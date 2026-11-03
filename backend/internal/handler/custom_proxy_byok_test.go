package handler

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"yingce/backend/internal/service"

	"github.com/gin-gonic/gin"
)

func TestCustomRelayAllowsHeaderOnlyAndUnauthenticatedUpstreams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, apiFormat := range []string{"openai", "gemini", "claude"} {
		for _, headerOnly := range []bool{false, true} {
			name := apiFormat + map[bool]string{true: "_header_only", false: "_no_auth"}[headerOnly]
			t.Run(name, func(t *testing.T) {
				upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if _, exists := r.Header["Authorization"]; exists {
						t.Error("relay emitted an empty Authorization header")
					}
					if _, exists := r.Header["X-Goog-Api-Key"]; exists {
						t.Error("relay emitted an empty Gemini API key header")
					}
					if headerOnly {
						if r.Header.Get("X-Gateway-Key") != "private-gateway-header" {
							t.Error("relay lost the custom authentication header")
						}
						if apiFormat == "claude" && r.Header.Get("x-api-key") != "private-claude-header" {
							t.Error("relay overwrote the custom Claude API key header")
						}
					}
					if r.URL.Query().Get("api_key") != "private-query-key" {
						t.Error("relay lost query authentication")
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"ok":true}`)
				}))
				defer upstream.Close()
				useCustomRelayTestClient(t, upstream.Client())
				path := "/v1/chat/completions"
				if apiFormat == "gemini" {
					path = "/v1beta/models/custom-model:generateContent"
				} else if apiFormat == "claude" {
					path = "/v1/messages"
				}
				request := httptest.NewRequest(http.MethodPost, "/api/ai/custom", strings.NewReader(`{"model":"custom-model"}`))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("X-Canvas-Upstream-URL", upstream.URL+path+"?api_key=private-query-key")
				request.Header.Set("X-Canvas-Upstream-Base-URL", upstream.URL+"?api_key=private-query-key")
				request.Header.Set("X-Canvas-Upstream-Format", apiFormat)
				if headerOnly {
					headers := `[{"name":"X-Gateway-Key","value":"private-gateway-header"}]`
					if apiFormat == "claude" {
						headers = `[{"name":"X-Gateway-Key","value":"private-gateway-header"},{"name":"x-api-key","value":"private-claude-header"}]`
					}
					request.Header.Set(service.CustomRelayHeadersHeader, base64.StdEncoding.EncodeToString([]byte(headers)))
				}
				prepareRelayTestRequest(t, request)
				response := httptest.NewRecorder()
				context, _ := gin.CreateTestContext(response)
				context.Request = request
				proxyCustomRelayRequest(context, defaultCustomRelayTestPolicy())
				if response.Code != http.StatusOK || response.Body.String() != `{"ok":true}` {
					t.Fatalf("status=%d, body=%s", response.Code, response.Body.String())
				}
			})
		}
	}
}

func TestCustomRelayRejectsMalformedExplicitAPIKey(t *testing.T) {
	for _, value := range []string{"Basic encoded", "Bearer", "Bearer ", "Bearer key\r\ninjected"} {
		if _, err := customRelayAPIKey(value); err == nil {
			t.Error("malformed explicit authorization was accepted")
		}
	}
}

func TestCustomRelayGatewayQueryKeepsEndpointRestrictions(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	for _, test := range []struct {
		name, target, base string
		allowed            bool
	}{
		{name: "fixed_auth", target: "https://127.0.0.1/v1/models?api_key=secret&region=cn", base: "https://127.0.0.1/v1?api_key=secret&region=cn", allowed: true},
		{name: "changed_auth", target: "https://127.0.0.1/v1/models?api_key=changed", base: "https://127.0.0.1/v1?api_key=secret"},
		{name: "cross_origin", target: "https://127.0.0.1/v1/models?api_key=secret", base: "https://127.0.0.1:8443/v1?api_key=secret"},
		{name: "unknown_dynamic_query", target: "https://127.0.0.1/v1/models?api_key=secret&redirect=elsewhere", base: "https://127.0.0.1/v1?api_key=secret"},
		{name: "unknown_endpoint", target: "https://127.0.0.1/account?api_key=secret", base: "https://127.0.0.1/v1?api_key=secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			target, _ := url.Parse(test.target)
			filtered, err := customRelayAuthorizationTarget(target, test.base)
			if err == nil {
				err = authorizeCustomRelay(http.MethodGet, filtered, "openai", "")
			}
			if (err == nil) != test.allowed {
				t.Fatalf("allowed=%v, err=%v", test.allowed, err)
			}
			if target.RawQuery == "" {
				t.Fatal("authorization filtering mutated the upstream URL")
			}
		})
	}
}
