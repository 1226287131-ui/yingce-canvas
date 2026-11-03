package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func TestRecordCustomProviderRequestRedactsEchoedCredentials(t *testing.T) {
	const apiKey = "private-bearer-credential"
	const headerValue = "private-gateway\"credential<>&"
	const queryValue = "private-query+credential&value"
	const secretKey = "private-signing-credential"
	const ordinary = "ordinary diagnostic: prompt is readable; application/json; test-agent; cn-east"
	echo := "key=" + apiKey + "; gateway=" + headerValue + "; query=" + queryValue + "; encoded=" + url.QueryEscape(queryValue) + "; signing=" + secretKey + "; " + ordinary
	for _, scenario := range []string{"plain-text-http-error", "plain-text-transport-error", "http200-business-error", "http200-content"} {
		t.Run(scenario, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "api-call-log.db")), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sqlDB.Close() })
			if err := db.AutoMigrate(&model.SystemSetting{}, &model.Asset{}, &model.CanvasProject{}, &model.Task{}, &model.TaskLog{}, &model.Result{}, &model.ApiCallLog{}, &model.TaskTextDelta{}, &model.ModelPricing{}); err != nil {
				t.Fatal(err)
			}
			service := &Service{repo: repository.New(db)}
			config := providerConfig{
				BaseURL: "https://provider.example/v1?api_key=" + url.QueryEscape(queryValue) + "&region=cn-east",
				APIKey:  apiKey, SecretKey: secretKey, APIFormat: "openai",
				Headers: []OutboundHeader{{Name: "X-Customer-Connection", Value: headerValue}},
			}
			ctx := withCustomProviderEndpoint(context.Background(), config)
			ctx = context.WithValue(ctx, providerAnalyticsKey{}, providerAnalyticsContext{Service: service, UserID: "user-1", Capability: "text", Model: "custom-text", RequestKind: "create"})
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, config.BaseURL+"&operation=chat", strings.NewReader(`{"prompt":"`+ordinary+`"}`))
			if err != nil {
				t.Fatal(err)
			}
			applyProviderAuth(request, config)
			ApplyOutboundHeaders(request, config.Headers)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("User-Agent", "test-agent")
			body := []byte(echo)
			status := http.StatusOK
			var requestErr error
			wantFailed := true
			switch scenario {
			case "plain-text-http-error":
				status = http.StatusBadRequest
				requestErr = providerHTTPError{StatusCode: status, Status: "400 Bad Request", Body: string(body)}
			case "plain-text-transport-error":
				status = 0
				requestErr = errors.New(echo)
			case "http200-business-error":
				body, _ = json.Marshal(map[string]any{"code": "invalid_request", "message": echo})
			case "http200-content":
				body, _ = json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": echo}}}})
				wantFailed = false
			}
			recordProviderRequest(request, time.Now(), status, body, requestErr)
			var logs []model.ApiCallLog
			if err := db.Find(&logs).Error; err != nil {
				t.Fatal(err)
			}
			if len(logs) != 1 {
				t.Fatalf("stored logs = %d, want 1", len(logs))
			}
			logged := logs[0]
			serialized, err := json.Marshal(logged)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{apiKey, headerValue, queryValue, url.QueryEscape(queryValue), secretKey} {
				if strings.Contains(string(serialized), secret) || strings.Contains(logged.ResponseBody, secret) || strings.Contains(logged.Error, secret) {
					t.Fatalf("known credential leaked into persisted API call log")
				}
				encoded, _ := json.Marshal(secret)
				if strings.Contains(logged.ResponseBody, string(encoded[1:len(encoded)-1])) {
					t.Fatal("JSON-escaped credential leaked into persisted API call log")
				}
			}
			if !strings.Contains(logged.ResponseBody, ordinary) || !strings.Contains(logged.ResponseBody, "[REDACTED]") {
				t.Fatalf("response diagnostics were not preserved with redaction: %q", logged.ResponseBody)
			}
			if !strings.Contains(logged.RequestBody, ordinary) || logged.RequestContentType != "application/json" {
				t.Fatalf("ordinary request data was altered: %q", logged.RequestBody)
			}
			if wantFailed != (logged.Status == model.ApiCallStatusFailed) {
				t.Fatalf("log status = %q, want failed %v", logged.Status, wantFailed)
			}
			if (scenario == "plain-text-transport-error" || scenario == "http200-business-error") && !strings.Contains(logged.Error, "[REDACTED]") {
				t.Fatalf("stored error did not exercise final credential redaction: %q", logged.Error)
			}
			if request.Header.Get("Authorization") != "Bearer "+apiKey || request.Header.Get("X-Customer-Connection") != headerValue || request.URL.Query().Get("api_key") != queryValue {
				t.Fatal("log redaction changed outbound credentials")
			}
			if !strings.Contains(string(body), apiKey) {
				t.Fatal("log redaction changed original upstream response")
			}
		})
	}
}

func TestCustomProviderLogRedactsRequestAuthWithoutConfigCredential(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://provider.example/v1?access_token=query-only-credential", nil)
	if err != nil {
		t.Fatal(err)
	}
	request = request.WithContext(withCustomProviderEndpoint(request.Context(), providerConfig{BaseURL: "https://provider.example/v1"}))
	request.Header.Set("Authorization", "Bearer request-only-credential")
	request.Header.Set("X-Api-Key", "header-only-credential")
	logged := model.ApiCallLog{ResponseBody: "auth=request-only-credential; header=header-only-credential; query=query-only-credential; keep readable", Error: "request-only-credential"}
	redactCustomProviderLogCredentials(request, &logged)
	if logged.ResponseBody != "auth=[REDACTED]; header=[REDACTED]; query=[REDACTED]; keep readable" || logged.Error != "[REDACTED]" {
		t.Fatalf("request credentials were not fully redacted: %#v", logged)
	}
}

func TestRecordCustomProviderRequestKeepsShortHeaderNumbersAndTaskIdentity(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "api-call-log.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.SystemSetting{}, &model.Asset{}, &model.CanvasProject{}, &model.Task{}, &model.TaskLog{}, &model.Result{}, &model.ApiCallLog{}, &model.TaskTextDelta{}, &model.ModelPricing{}); err != nil {
		t.Fatal(err)
	}
	service := &Service{repo: repository.New(db)}
	task := model.Task{ID: "local-task-1", UserID: "user-1", Type: "canvas_audio", Status: model.TaskStatusRunning}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	config := providerConfig{
		BaseURL: "https://provider.example/v1/jobs/1", APIKey: "strict-private-key", APIFormat: "openai",
		Headers: []OutboundHeader{{Name: "X-Feature", Value: "1"}, {Name: "X-Region", Value: "cn"}, {Name: "X-Customer-Connection", Value: "gate"}},
	}
	ctx := withCustomProviderEndpoint(context.Background(), config)
	ctx = context.WithValue(ctx, providerAnalyticsKey{}, providerAnalyticsContext{Service: service, UserID: task.UserID, TaskID: task.ID, Capability: "audio", Model: "custom-audio", RequestKind: "create"})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, config.BaseURL, strings.NewReader(`{"count":1,"region":"cn-east","prompt":"provider-task-111"}`))
	if err != nil {
		t.Fatal(err)
	}
	applyProviderAuth(request, config)
	ApplyOutboundHeaders(request, config.Headers)
	request.Header.Set("Content-Type", "application/json")
	body := []byte(`{"id":"provider-task-111","status":"pending","count":111,"limit":1,"region":"cn-east","message":"feature=1; region=cn; auth=gate; private=strict-private-key"}`)
	recordProviderRequest(request, time.Now(), http.StatusOK, body, nil)
	var logged model.ApiCallLog
	if err := db.First(&logged).Error; err != nil {
		t.Fatal(err)
	}
	if logged.ProviderRequestID != "provider-task-111" || logged.ProviderStatus != "pending" || logged.Path != "/v1/jobs/1" || logged.UpstreamURL != config.BaseURL {
		t.Fatalf("machine identifiers were changed: %#v", logged)
	}
	var storedTask model.Task
	if err := db.First(&storedTask, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedTask.ProviderRequestID != "provider-task-111" {
		t.Fatalf("persisted task provider identity = %q", storedTask.ProviderRequestID)
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(logged.ResponseBody), &response); err != nil {
		t.Fatalf("logged response is not valid JSON: %v", err)
	}
	if response["count"] != float64(111) || response["limit"] != float64(1) || response["id"] != "provider-task-111" || response["region"] != "cn-east" {
		t.Fatalf("ordinary response values were changed: %#v", response)
	}
	if response["message"] != "feature=[REDACTED]; region=[REDACTED]; auth=[REDACTED]; private=[REDACTED]" {
		t.Fatalf("standalone custom Header values were not redacted: %q", response["message"])
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(logged.RequestBody), &input); err != nil {
		t.Fatalf("logged request is not valid JSON: %v", err)
	}
	if input["count"] != float64(1) || input["region"] != "cn-east" || input["prompt"] != "provider-task-111" {
		t.Fatalf("ordinary request values were changed: %#v", input)
	}
}

func TestCustomProviderLogNeverChangesMachineFields(t *testing.T) {
	config := providerConfig{BaseURL: "https://provider.example/v1/1", APIKey: "1", SecretKey: "cn"}
	request, err := http.NewRequest(http.MethodPost, config.BaseURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	request = request.WithContext(withCustomProviderEndpoint(request.Context(), config))
	logged := model.ApiCallLog{
		ProviderRequestID: "provider-task-111", ProviderStatus: "cn-pending", Path: "/v1/1", UpstreamURL: config.BaseURL,
		ErrorCode: "code-1-cn", ResponseBody: `{"count":1,"content":"1 cn"}`, Error: "1 cn",
	}
	redactCustomProviderLogCredentials(request, &logged)
	if logged.ProviderRequestID != "provider-task-111" || logged.ProviderStatus != "cn-pending" || logged.Path != "/v1/1" || logged.UpstreamURL != config.BaseURL || logged.ErrorCode != "code-1-cn" {
		t.Fatalf("machine fields were altered: %#v", logged)
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(logged.ResponseBody), &response); err != nil {
		t.Fatal(err)
	}
	if response["count"] != "[REDACTED]" || response["content"] != "[REDACTED] [REDACTED]" || logged.Error != "[REDACTED] [REDACTED]" {
		t.Fatalf("short explicit credentials were not strictly hidden: %#v", logged)
	}
}
