package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func (s *Service) resolveTimelineTranscriptionConfig(config providerConfig) (providerConfig, error) {
	if err := s.RequireFeature(FeatureCustomChannels); err != nil {
		return providerConfig{}, err
	}
	if strings.TrimSpace(config.ChannelID) != "" || systemChannelIDFromBaseURL(config.BaseURL) != "" {
		return providerConfig{}, BadAuthRequest("字幕转写仅支持自定义 OpenAI 兼容转写模型")
	}
	if strings.TrimSpace(config.Model) == "" || strings.TrimSpace(config.BaseURL) == "" {
		return providerConfig{}, BadAuthRequest("请填写自定义转写模型名称和接口地址")
	}
	if config.APIFormat != "" && !strings.EqualFold(config.APIFormat, "openai") {
		return providerConfig{}, BadAuthRequest("字幕转写需要支持 /audio/transcriptions 的 OpenAI 兼容接口")
	}
	config.APIFormat = "openai"
	return s.resolveProviderConfig(config)
}

// User-selected transcription uses the same guarded transport as generation.
// Only owned resource audio is uploaded; client-supplied download URLs are never used.
func transcribeCustomAudio(ctx context.Context, config providerConfig, wavPath, language string) ([]timelineTranscriptionSegment, string, error) {
	ctx = withCustomProviderEndpoint(ctx, config)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	audio, err := os.Open(wavPath)
	if err != nil {
		return nil, "", errors.New("读取待转写音频失败")
	}
	defer audio.Close()
	part, err := writer.CreateFormFile("file", filepath.Base(wavPath))
	if err != nil {
		return nil, "", errors.New("准备转写音频失败")
	}
	if _, err := io.Copy(part, audio); err != nil {
		return nil, "", errors.New("读取待转写音频失败")
	}
	fields := map[string]string{"model": config.Model, "response_format": "verbose_json", "timestamp_granularities[]": "segment"}
	if strings.TrimSpace(language) != "" {
		fields["language"] = strings.TrimSpace(language)
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, "", errors.New("准备转写请求失败")
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", errors.New("准备转写请求失败")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL(config.BaseURL, "/audio/transcriptions"), &body)
	if err != nil {
		return nil, "", errors.New("自定义转写接口地址无效")
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if strings.TrimSpace(config.APIKey) != "" {
		applyProviderAuth(req, config)
	}
	ApplyOutboundHeaders(req, config.Headers)
	payload, _, err := doBinary(req)
	if err != nil {
		var httpErr providerHTTPError
		if errors.As(err, &httpErr) {
			return nil, "", fmt.Errorf("自定义转写服务返回 HTTP %d，请检查模型和鉴权配置", httpErr.StatusCode)
		}
		return nil, "", errors.New("无法调用自定义转写服务，请检查接口地址或稍后重试")
	}
	segments, detectedLanguage, err := decodeWhisperVerboseJSON(payload)
	if err != nil {
		return nil, "", errors.New("自定义转写服务返回了无效结果，需要 verbose_json 格式")
	}
	if len(segments) == 0 {
		return nil, "", errors.New("自定义转写服务未返回带时间戳的语音段落，请选择支持 verbose_json/segments 的模型")
	}
	return segments, detectedLanguage, nil
}
