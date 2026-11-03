package handler

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCustomRelayAsyncAudioLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	var requests []string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer async-audio-test-key" || r.URL.Query().Get("api_key") != "fixed-gateway-key" {
			t.Error("async audio request lost its configured authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/audio/tasks":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["model"] != "customer-audio" || body["input"] != "test speech" {
				t.Error("async audio creation payload was not preserved")
			}
			_, _ = io.WriteString(w, `{"id":"audio-task-1","status":"pending"}`)
		case "GET /v1/audio/tasks/audio-task-1":
			_, _ = io.WriteString(w, `{"id":"audio-task-1","status":"completed"}`)
		case "GET /v1/audio/tasks/audio-task-1/content":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = io.WriteString(w, "async-audio-content")
		default:
			t.Errorf("unexpected async audio request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	useCustomRelayTestClient(t, upstream.Client())

	for _, step := range []struct {
		method, path, body, wantBody, wantType string
	}{
		{http.MethodPost, "/v1/audio/tasks", `{"model":"customer-audio","input":"test speech"}`, `{"id":"audio-task-1","status":"pending"}`, "application/json"},
		{http.MethodGet, "/v1/audio/tasks/audio-task-1", "", `{"id":"audio-task-1","status":"completed"}`, "application/json"},
		{http.MethodGet, "/v1/audio/tasks/audio-task-1/content", "", "async-audio-content", "audio/mpeg"},
	} {
		request := httptest.NewRequest(step.method, "/api/ai/custom", strings.NewReader(step.body))
		request.Header.Set("Authorization", "Bearer async-audio-test-key")
		if step.method == http.MethodPost {
			request.Header.Set("Content-Type", "application/json")
		}
		request.Header.Set("X-Canvas-Upstream-URL", upstream.URL+step.path+"?api_key=fixed-gateway-key")
		request.Header.Set("X-Canvas-Upstream-Base-URL", upstream.URL+"/v1?api_key=fixed-gateway-key")
		request.Header.Set("X-Canvas-Upstream-Format", "openai")
		prepareRelayTestRequest(t, request)
		response := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(response)
		context.Request = request
		proxyCustomRelayRequest(context, defaultCustomRelayTestPolicy())
		mediaType, _, err := mime.ParseMediaType(response.Header().Get("Content-Type"))
		if err != nil || response.Code != http.StatusOK || mediaType != step.wantType || response.Body.String() != step.wantBody {
			t.Fatalf("async audio step %s %s failed: status=%d, type=%q", step.method, step.path, response.Code, response.Header().Get("Content-Type"))
		}
	}
	if len(requests) != 3 {
		t.Fatalf("expected one upstream call per async audio stage, got %d", len(requests))
	}
}

func TestAuthorizeCustomRelayAsyncAudioRestrictions(t *testing.T) {
	for _, test := range []struct {
		name, method, path, format, contentType string
		allowed                                 bool
	}{
		{"create", http.MethodPost, "/v1/audio/tasks", "openai", "application/json", true},
		{"poll", http.MethodGet, "/v1/audio/tasks/task-1", "openai", "", true},
		{"content", http.MethodGet, "/v1/audio/tasks/task-1/content", "openai", "", true},
		{"nested_base", http.MethodGet, "/gateway/api/v1/audio/tasks/task-1/content", "openai", "", true},
		{"list", http.MethodGet, "/v1/audio/tasks", "openai", "", false},
		{"delete", http.MethodDelete, "/v1/audio/tasks/task-1", "openai", "", false},
		{"post_task", http.MethodPost, "/v1/audio/tasks/task-1", "openai", "application/json", false},
		{"post_content", http.MethodPost, "/v1/audio/tasks/task-1/content", "openai", "application/json", false},
		{"form_create", http.MethodPost, "/v1/audio/tasks", "openai", "multipart/form-data; boundary=test", false},
		{"wrong_format", http.MethodGet, "/v1/audio/tasks/task-1", "gemini", "", false},
		{"dynamic_query", http.MethodGet, "/v1/audio/tasks/task-1?redirect=elsewhere", "openai", "", false},
		{"key_in_query", http.MethodGet, "/v1/audio/tasks/task-1?api_key=untrusted", "openai", "", false},
		{"traversal", http.MethodGet, "/v1/audio/tasks/%2e%2e/content", "openai", "", false},
		{"nested_id", http.MethodGet, "/v1/audio/tasks/task-1/nested/content", "openai", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			target, err := url.Parse("https://api.example.com" + test.path)
			if err != nil {
				t.Fatal(err)
			}
			if err := authorizeCustomRelay(test.method, target, test.format, test.contentType); (err == nil) != test.allowed {
				t.Fatalf("allowed=%v, err=%v", test.allowed, err)
			}
		})
	}
}
