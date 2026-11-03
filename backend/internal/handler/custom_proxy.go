package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
)

const maxCustomRelayErrorResponseBytes int64 = 64 << 10

var customRelayClient = service.CustomRelayHTTPClient

func RegisterCustomRelayRoutes(r *gin.RouterGroup, svc *service.Service) {
	r.Any("/ai/custom", func(c *gin.Context) {
		user, err := currentUser(c, svc)
		if err != nil {
			failService(c, err)
			return
		}
		if err := svc.RequireFeature(service.FeatureCustomChannels); err != nil {
			failService(c, err)
			return
		}
		policy, available := loadRuntimePolicy(c, svc)
		if !available || !enforceRateLimit(c, "custom-relay:"+user.ID, policy.Request.CustomRelayPerMinute, time.Minute) {
			return
		}
		ttl := time.Duration(policy.Request.CustomRelayTimeoutMinutes+1) * time.Minute
		release, acquired, err := svc.AcquireCustomRelaySlot(c.Request.Context(), user.ID, policy.Request.CustomRelayConcurrency, ttl)
		if err != nil {
			fail(c, http.StatusServiceUnavailable, errors.New("自定义渠道并发协调服务不可用"))
			return
		}
		if !acquired {
			fail(c, http.StatusTooManyRequests, errors.New("自定义渠道并发请求过多，请等待已有请求完成"))
			return
		}
		defer release()
		proxyCustomRelayRequestWithService(c, policy.Request, svc)
	})
}

func proxyCustomRelayRequest(c *gin.Context, policy service.RuntimeRequestPolicy) {
	proxyCustomRelayRequestWithService(c, policy, nil)
}

func proxyCustomRelayRequestWithService(c *gin.Context, policy service.RuntimeRequestPolicy, svc *service.Service) {
	target, err := service.ValidateCustomRelayURL(c.GetHeader("X-Canvas-Upstream-URL"))
	if err != nil {
		failService(c, err)
		return
	}
	apiFormat := strings.ToLower(strings.TrimSpace(c.GetHeader("X-Canvas-Upstream-Format")))
	if apiFormat == "" {
		apiFormat = "openai"
	}
	authorizationTarget, err := customRelayAuthorizationTarget(target, c.GetHeader("X-Canvas-Upstream-Base-URL"))
	if err != nil {
		fail(c, http.StatusForbidden, err)
		return
	}
	if err := authorizeCustomRelay(c.Request.Method, authorizationTarget, apiFormat, c.GetHeader("Content-Type")); err != nil {
		fail(c, http.StatusForbidden, err)
		return
	}
	apiKey, err := customRelayAPIKey(c.GetHeader("Authorization"))
	if err != nil {
		fail(c, http.StatusUnauthorized, err)
		return
	}
	headers, err := service.DecodeRelayOutboundHeaders(c.GetHeader(service.CustomRelayHeadersHeader))
	if err != nil {
		failService(c, err)
		return
	}
	requestLimit := policy.CustomRelayRequestMB << 20
	if c.Request.ContentLength > requestLimit {
		fail(c, http.StatusRequestEntityTooLarge, errors.New("自定义渠道请求超过配置上限"))
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, requestLimit)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			fail(c, http.StatusRequestEntityTooLarge, errors.New("自定义渠道请求超过配置上限"))
			return
		}
		fail(c, http.StatusBadRequest, errors.New("读取自定义渠道请求失败"))
		return
	}
	if c.Request.Method == http.MethodGet && len(body) != 0 {
		fail(c, http.StatusBadRequest, errors.New("模型列表请求不允许携带请求体"))
		return
	}
	upstreamReq, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		fail(c, http.StatusBadRequest, errors.New("构造自定义渠道请求失败"))
		return
	}
	if contentType := c.GetHeader("Content-Type"); contentType != "" {
		upstreamReq.Header.Set("Content-Type", contentType)
	}
	if strings.Contains(strings.ToLower(c.GetHeader("Accept")), "text/event-stream") {
		upstreamReq.Header.Set("Accept", "text/event-stream")
	} else {
		upstreamReq.Header.Set("Accept", "application/json")
	}
	service.ApplyOutboundHeaders(upstreamReq, headers)
	service.ApplyDefaultOutboundHeaders(upstreamReq)
	if apiFormat == "gemini" {
		if apiKey != "" {
			upstreamReq.Header.Set("x-goog-api-key", apiKey)
		}
	} else if apiFormat == "claude" {
		if apiKey != "" {
			upstreamReq.Header.Set("x-api-key", apiKey)
		}
		upstreamReq.Header.Set("anthropic-version", "2023-06-01")
	} else if apiKey != "" {
		upstreamReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := customRelayClient(time.Duration(policy.CustomRelayTimeoutMinutes) * time.Minute).Do(upstreamReq)
	if err != nil {
		fail(c, http.StatusBadGateway, errors.New(userFacingRelayError(svc, errors.New("自定义渠道上游连接失败"))))
		return
	}
	defer resp.Body.Close()
	allowBinary := customVideoContentPath.MatchString(target.EscapedPath()) || customAudioContentPath.MatchString(target.EscapedPath()) || strings.HasSuffix(target.Path, "/audio/speech")
	writeCustomRelayResponse(c, svc, resp, apiKey, policy.CustomRelayResponseMB<<20, allowBinary)
}

func writeCustomRelayResponse(c *gin.Context, svc *service.Service, resp *http.Response, apiKey string, responseLimit int64, allowBinary bool) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		writeCustomRelayError(c, svc, resp, apiKey, mediaType)
		return
	}
	if mediaType == "text/event-stream" {
		c.Header("Content-Type", "text/event-stream; charset=utf-8")
		c.Header("X-Accel-Buffering", "no")
		c.Status(resp.StatusCode)
		c.Writer.WriteHeaderNow()
		copyCustomRelayStream(c, resp.Body, apiKey, responseLimit)
		return
	}
	if allowBinary && (strings.HasPrefix(mediaType, "video/") || strings.HasPrefix(mediaType, "audio/") || mediaType == "application/octet-stream") {
		body, err := readLimitedRelayBody(resp.Body, responseLimit)
		if err != nil {
			fail(c, http.StatusBadGateway, errors.New(userFacingRelayError(svc, errors.New("自定义渠道上游返回了过大的媒体文件"))))
			return
		}
		c.Data(resp.StatusCode, mediaType, body)
		return
	}
	if mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
		fail(c, http.StatusBadGateway, errors.New(userFacingRelayError(svc, errors.New("自定义渠道上游返回了不支持的内容类型"))))
		return
	}
	limit := responseLimit
	body, err := readLimitedRelayBody(resp.Body, limit)
	if err != nil || !json.Valid(body) {
		fail(c, http.StatusBadGateway, errors.New(userFacingRelayError(svc, errors.New("自定义渠道上游返回无效或过大的 JSON"))))
		return
	}
	body = redactRelaySecret(body, apiKey)
	c.Data(resp.StatusCode, "application/json; charset=utf-8", body)
}

func writeCustomRelayError(c *gin.Context, svc *service.Service, resp *http.Response, apiKey string, mediaType string) {
	body, err := readLimitedRelayBody(resp.Body, maxCustomRelayErrorResponseBytes)
	if err != nil {
		fail(c, http.StatusBadGateway, errors.New(userFacingRelayError(svc, errors.New("自定义渠道上游请求失败"))))
		return
	}
	body = redactRelaySecret(body, apiKey)
	rawMessage := strings.TrimSpace(string(body))
	if intercepted := interceptedRelayText(svc, rawMessage); intercepted != rawMessage {
		fail(c, resp.StatusCode, errors.New(intercepted))
		return
	}
	if (mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")) && json.Valid(body) {
		c.Data(resp.StatusCode, "application/json; charset=utf-8", body)
		return
	}
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 200 {
		snippet = snippet[:200] + "..."
	}
	fail(c, resp.StatusCode, errors.New(userFacingRelayError(svc, fmt.Errorf("自定义渠道上游请求失败（%s）%s", resp.Status, snippet))))
}

func userFacingRelayError(svc *service.Service, err error) string {
	if svc == nil {
		return err.Error()
	}
	return svc.UserFacingErrorMessage(err)
}

func interceptedRelayText(svc *service.Service, raw string) string {
	if svc == nil {
		return raw
	}
	return svc.InterceptResponseText(raw)
}

func copyCustomRelayStream(c *gin.Context, source io.Reader, apiKey string, maxBytes int64) {
	redactor := newRelayStreamRedactor(apiKey)
	buffer := make([]byte, 32<<10)
	var written int64
	for written < maxBytes {
		read, err := source.Read(buffer)
		if read > 0 {
			remaining := maxBytes - written
			if int64(read) > remaining {
				read = int(remaining)
			}
			chunk := redactor.Push(buffer[:read], false)
			if len(chunk) > 0 {
				if _, writeErr := c.Writer.Write(chunk); writeErr != nil {
					return
				}
				c.Writer.Flush()
			}
			written += int64(read)
		}
		if err != nil {
			break
		}
	}
	if tail := redactor.Push(nil, true); len(tail) > 0 {
		_, _ = c.Writer.Write(tail)
		c.Writer.Flush()
	}
}

func readLimitedRelayBody(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("response body is too large")
	}
	return data, nil
}

func customRelayAPIKey(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		// 浏览器登录态由路由独立校验；上游可仅使用自定义 Header 或无需鉴权。
		return "", nil
	}
	scheme, apiKey, found := strings.Cut(strings.TrimSpace(value), " ")
	apiKey = strings.TrimSpace(apiKey)
	if !found || !strings.EqualFold(scheme, "Bearer") || apiKey == "" || len(apiKey) > 512 || strings.ContainsAny(apiKey, "\r\n") {
		return "", errors.New("自定义渠道 API Key 无效")
	}
	return apiKey, nil
}

// 配置在 Base URL 中的网关参数原样透传；协议许可检查仍仅接受既有路径和动态参数。
func customRelayAuthorizationTarget(target *url.URL, rawBaseURL string) (*url.URL, error) {
	if strings.TrimSpace(rawBaseURL) == "" {
		return target, nil
	}
	base, err := service.ValidateCustomRelayURL(rawBaseURL)
	if err != nil || !strings.EqualFold(base.Scheme, target.Scheme) || !strings.EqualFold(base.Host, target.Host) {
		return nil, errors.New("自定义渠道请求地址必须与 Base URL 使用相同服务")
	}
	baseQuery, err := url.ParseQuery(base.RawQuery)
	if err != nil {
		return nil, errors.New("自定义渠道 Base URL 查询参数无效")
	}
	query, err := url.ParseQuery(target.RawQuery)
	if err != nil {
		return nil, errors.New("自定义渠道查询参数无效")
	}
	for key, values := range baseQuery {
		targetValues := query[key]
		if len(targetValues) != len(values) {
			return nil, errors.New("自定义渠道请求的网关参数与 Base URL 不一致")
		}
		for index, value := range values {
			if targetValues[index] != value {
				return nil, errors.New("自定义渠道请求的网关参数与 Base URL 不一致")
			}
		}
		delete(query, key)
	}
	filtered := *target
	filtered.RawQuery = query.Encode()
	return &filtered, nil
}

func redactRelaySecret(body []byte, apiKey string) []byte {
	if apiKey == "" {
		return body
	}
	return bytes.ReplaceAll(body, []byte(apiKey), []byte("[REDACTED]"))
}

type relayStreamRedactor struct {
	secret  []byte
	pending []byte
}

func newRelayStreamRedactor(secret string) *relayStreamRedactor {
	return &relayStreamRedactor{secret: []byte(secret)}
}

func (r *relayStreamRedactor) Push(chunk []byte, final bool) []byte {
	r.pending = append(r.pending, chunk...)
	if len(r.secret) == 0 {
		result := append([]byte(nil), r.pending...)
		r.pending = r.pending[:0]
		return result
	}
	r.pending = bytes.ReplaceAll(r.pending, r.secret, []byte("[REDACTED]"))
	if final {
		result := append([]byte(nil), r.pending...)
		r.pending = r.pending[:0]
		return result
	}
	keep := relaySecretPrefixSuffixLength(r.pending, r.secret)
	cut := len(r.pending) - keep
	result := append([]byte(nil), r.pending[:cut]...)
	r.pending = append(r.pending[:0], r.pending[cut:]...)
	return result
}

func relaySecretPrefixSuffixLength(data []byte, secret []byte) int {
	limit := len(secret) - 1
	if len(data) < limit {
		limit = len(data)
	}
	for length := limit; length > 0; length-- {
		if bytes.Equal(data[len(data)-length:], secret[:length]) {
			return length
		}
	}
	return 0
}
