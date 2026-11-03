package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/protocol"
)

func assertCustomAPISecretsAbsent(t *testing.T, value string) {
	t.Helper()
	for _, secret := range []string{"private-api-key", "private-secret-key", "private-gateway-header", "private-query-key", "https://8.8.8.8"} {
		if strings.Contains(value, secret) {
			t.Fatalf("private connection value leaked: %s", secret)
		}
	}
}

func TestCustomAPITaskSecretsRoundTrip(t *testing.T) {
	s := &Service{dataDir: t.TempDir()}
	original := map[string]any{
		"config":     map[string]any{"baseUrl": "https://8.8.8.8/v1?api_key=private-query-key", "apiKey": "private-api-key", "secretKey": "private-secret-key", "headers": []any{map[string]any{"name": "X-Gateway-Key", "value": "private-gateway-header"}}},
		"cloudAgent": map[string]any{"customModels": []any{map[string]any{"config": map[string]any{"baseUrl": "https://8.8.8.8/v1", "apiKey": "system", "headers": []any{map[string]any{"name": "X-Gateway-Key", "value": "private-gateway-header"}}}}}},
		"value":      "ordinary-value",
	}
	input, err := normalizeTaskInput(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.protectTaskSecrets(input); err != nil {
		t.Fatal(err)
	}
	first, _ := json.Marshal(input)
	assertCustomAPISecretsAbsent(t, string(first))
	if strings.Contains(string(first), `"apiKey":"system"`) {
		t.Fatal("literal system credential was not encrypted")
	}
	if err := s.protectTaskSecrets(input); err != nil {
		t.Fatal(err)
	}
	second, _ := json.Marshal(input)
	if string(first) != string(second) {
		t.Fatal("already protected values were re-encrypted")
	}
	decrypted, err := s.decryptTaskInputJSON(string(second))
	if err != nil {
		t.Fatal(err)
	}
	var restored map[string]any
	if err := json.Unmarshal([]byte(decrypted), &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored, original) {
		t.Fatal("encrypted task input did not round-trip its JSON shape")
	}
	assertCustomAPISecretsAbsent(t, publicTaskInputJSON(string(second)))
}

func customCreationTextRequest() CreateTaskRequest {
	req := creationTextRequest()
	req.Input["config"] = map[string]any{
		"model": "custom-text", "apiFormat": "openai", "interfaceType": string(model.ChannelInterfaceChatCompletion),
		"baseUrl": "https://8.8.8.8/v1?api_key=private-query-key", "apiKey": "private-api-key", "secretKey": "private-secret-key",
		"headers": []any{map[string]any{"name": "X-Gateway-Key", "value": "private-gateway-header"}},
	}
	return req
}

func TestCustomAPICreationLifecycleNoPlatformCharge(t *testing.T) {
	s, db, id, guard := creationTestService(t)
	request := customCreationTextRequest()
	item, err := s.PrepareCreationSubmission("user", id, CreationRequest{CreationGuard: guard, ItemKey: "custom-text", Request: request})
	if err != nil {
		t.Fatal(err)
	}
	if item.Quote.BillingMode != "free" || item.Quote.AmountMicrocredits != 0 {
		t.Fatalf("custom quote = %#v", item.Quote)
	}
	duplicate, err := s.PrepareCreationSubmission("user", id, CreationRequest{CreationGuard: guard, ItemKey: "custom-text", Request: request})
	if err != nil || duplicate.ID != item.ID {
		t.Fatalf("prepare retry was not idempotent: %v", err)
	}
	stored, err := s.repo.CreationSubmission("user", id, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertCustomAPISecretsAbsent(t, stored.RequestJSON+stored.QuoteJSON+stored.PriceSignature)
	response, _ := json.Marshal(item)
	assertCustomAPISecretsAbsent(t, string(response))
	var decoded CreateTaskRequest
	if err := s.decodeCreationRequest(stored.RequestJSON, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Input["config"].(map[string]any)["apiKey"] != "private-api-key" {
		t.Fatal("creation snapshot credentials were not restored")
	}
	if _, err := s.GetCreationRun("other", id); err == nil {
		t.Fatal("foreign user read a creation run")
	}
	if _, err := s.ApproveCreationSubmissions("other", id, CreationRequest{CreationGuard: guard, SubmissionIDs: []string{item.ID}}); err == nil {
		t.Fatal("foreign user approved a custom creation")
	}
	if _, err := s.ExecuteCreationSubmission("other", id, CreationRequest{CreationGuard: guard, SubmissionID: item.ID}); err == nil {
		t.Fatal("foreign user executed a custom creation")
	}
	refreshed, err := s.RefreshCreationSubmission("user", id, CreationRequest{CreationGuard: guard, SubmissionID: item.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveCreationSubmissions("user", id, CreationRequest{CreationGuard: guard, SubmissionIDs: []string{refreshed.ID}}); err != nil {
		t.Fatal(err)
	}
	task, err := s.ExecuteCreationSubmission("user", id, CreationRequest{CreationGuard: guard, SubmissionID: refreshed.ID})
	if err != nil {
		t.Fatal(err)
	}
	if task.BillingOrderID != "" {
		t.Fatal("custom task reserved platform credits")
	}
	response, _ = json.Marshal(task)
	assertCustomAPISecretsAbsent(t, string(response))
	queued, err := s.repo.TaskForUser("user", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertCustomAPISecretsAbsent(t, queued.InputJSON)
	var orderCount int64
	if err := db.Model(&model.BillingOrder{}).Where("user_id = ?", "user").Count(&orderCount).Error; err != nil || orderCount != 0 {
		t.Fatalf("custom call created platform orders: %d, %v", orderCount, err)
	}
	var account model.CreditAccount
	if err := db.First(&account, "user_id = ?", "user").Error; err != nil || account.AvailableMicrocredits != 10000 {
		t.Fatalf("custom call changed platform balance: %#v, %v", account, err)
	}
}

func TestCustomAPIHeaderOnlyTaskIsUnbilled(t *testing.T) {
	s, _, _, _ := creationTestService(t)
	req := customCreationTextRequest()
	delete(req.Input["config"].(map[string]any), "apiKey")
	if !taskInputUsesCustomChannel(req.Input) {
		t.Fatal("header-only custom model was not recognized")
	}
	order, err := s.taskBillingOrder("user", &model.Task{ID: "custom", Type: "canvas_text"}, req.Input)
	if err != nil || order != nil {
		t.Fatalf("header-only custom billing = %#v, %v", order, err)
	}
}

func TestCustomAPITimelineAdmissionIsolation(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "")
	s, db := newTimelineTaskTestService(t)
	s.dataDir = t.TempDir()
	seedResource(t, db, "private-audio", "owner", "audio/wav")
	config := providerConfig{BaseURL: "https://8.8.8.8/v1?api_key=private-query-key", APIKey: "private-api-key", SecretKey: "private-secret-key", APIFormat: "openai", Model: "whisper-1", Headers: []OutboundHeader{{Name: "X-Gateway-Key", Value: "private-gateway-header"}}}
	if _, err := s.CreateTimelineTranscriptionTask("other", TimelineTranscriptionCreateRequest{ResourceID: "private-audio", Config: &config}); err == nil {
		t.Fatal("foreign audio accepted")
	}
	private := config
	private.BaseURL = "https://127.0.0.1/v1"
	if _, err := s.CreateTimelineTranscriptionTask("owner", TimelineTranscriptionCreateRequest{ResourceID: "private-audio", Config: &private}); err == nil {
		t.Fatal("private upstream accepted without an exact allowlist")
	}
	config.APIKey = ""
	task, err := s.CreateTimelineTranscriptionTask("owner", TimelineTranscriptionCreateRequest{ResourceID: "private-audio", Config: &config})
	if err != nil {
		t.Fatal(err)
	}
	if task.Provider != "custom" || task.Model != "whisper-1" || task.BillingOrderID != "" {
		t.Fatalf("custom transcription task = %#v", task)
	}
	queued, err := s.repo.TaskForUser("owner", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertCustomAPISecretsAbsent(t, queued.InputJSON)
	response, _ := json.Marshal(task)
	assertCustomAPISecretsAbsent(t, string(response))
	plain, err := s.decryptTaskInputJSON(queued.InputJSON)
	if err != nil {
		t.Fatal(err)
	}
	var input timelineTranscriptionInput
	if err := json.Unmarshal([]byte(plain), &input); err != nil || input.Config.Headers[0].Value != "private-gateway-header" {
		t.Fatal("custom transcription snapshot did not round-trip")
	}
	if err := s.repo.SaveSystemSetting(&model.SystemSetting{Key: "feature_availability", ValueJSON: `{"timelineTranscriptionEnabled":true,"customChannelsEnabled":false}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTimelineTranscriptionTask("owner", TimelineTranscriptionCreateRequest{ResourceID: "private-audio", Config: &config}); err == nil {
		t.Fatal("custom transcription ignored the administrator feature gate")
	}
}

func TestCustomAPITimelineTransport(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	for _, apiKey := range []string{"private-api-key", ""} {
		t.Run(map[bool]string{true: "header_only", false: "api_key"}[apiKey == ""], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/audio/transcriptions" || r.URL.Query().Get("api_key") != "private-query-key" {
					t.Errorf("wrong custom transcription endpoint")
				}
				wantAuth := ""
				if apiKey != "" {
					wantAuth = "Bearer " + apiKey
				}
				if r.Header.Get("Authorization") != wantAuth || r.Header.Get("X-Gateway-Key") != "private-gateway-header" {
					t.Error("custom authentication was not applied")
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					http.Error(w, "invalid multipart", http.StatusBadRequest)
					return
				}
				defer r.MultipartForm.RemoveAll()
				if r.FormValue("model") != "whisper-1" || r.FormValue("response_format") != "verbose_json" || r.FormValue("language") != "zh" || r.FormValue("timestamp_granularities[]") != "segment" {
					t.Error("incorrect transcription multipart fields")
				}
				file, _, err := r.FormFile("file")
				if err != nil {
					t.Error(err)
				} else {
					data, _ := io.ReadAll(file)
					file.Close()
					if string(data) != "test-wav" {
						t.Error("audio bytes changed")
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"language":"zh","segments":[{"start":0.25,"end":1.5,"text":"你好"}]}`)
			}))
			defer server.Close()
			wavPath := filepath.Join(t.TempDir(), "audio.wav")
			if err := os.WriteFile(wavPath, []byte("test-wav"), 0600); err != nil {
				t.Fatal(err)
			}
			config := providerConfig{BaseURL: server.URL + "/v1?api_key=private-query-key", APIFormat: "openai", Model: "whisper-1", APIKey: apiKey, Headers: []OutboundHeader{{Name: "X-Gateway-Key", Value: "private-gateway-header"}}}
			segments, language, err := transcribeCustomAudio(context.Background(), config, wavPath, "zh")
			if err != nil || language != "zh" || len(segments) != 1 || segments[0].StartMs != 250 || segments[0].EndMs != 1500 || segments[0].Text != "你好" {
				t.Fatalf("custom transcription result = %#v, %q, %v", segments, language, err)
			}
		})
	}
}

func TestCustomAPIURLQueries(t *testing.T) {
	got := ChannelAPIURL("https://provider.example/gateway/v1?api_key=private-query-key&region=cn", "/v2/tasks?cursor=next")
	parsed, err := url.Parse(got)
	if err != nil || parsed.Path != "/gateway/v2/tasks" || parsed.Query().Get("api_key") != "private-query-key" || parsed.Query().Get("region") != "cn" || parsed.Query().Get("cursor") != "next" {
		t.Fatal("channel path joining did not preserve gateway query authentication")
	}
	for name, resolve := range map[string]func() (string, error){
		"agnes": func() (string, error) {
			return ChannelAPIURLForProtocol("https://provider.example/v1?api_key=private-query-key&region=old", "/agnesapi?video_id=video-1&region=cn", model.ChannelInterfaceAgnesVideo), nil
		},
		"protocol_origin": func() (string, error) {
			return protocolRequestURL("https://provider.example/v1?api_key=private-query-key&region=old", protocol.RequestSpec{Path: "/agnesapi?video_id=video-1&region=cn", OriginPath: true})
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := resolve()
			parsed, parseErr := url.Parse(got)
			if err != nil || parseErr != nil || parsed.Path != "/agnesapi" || parsed.Query().Get("api_key") != "private-query-key" || parsed.Query().Get("region") != "cn" || parsed.Query().Get("video_id") != "video-1" {
				t.Fatal("origin-path query authentication was lost")
			}
		})
	}
}

func TestCustomAPIModelCatalogWithoutAPIKey(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	t.Setenv("ENABLE_PROVIDER_PLUGINS", "false")
	for _, apiFormat := range []string{"openai", "gemini"} {
		for _, headerOnly := range []bool{false, true} {
			name := apiFormat + map[bool]string{true: "_header_only", false: "_no_auth"}[headerOnly]
			t.Run(name, func(t *testing.T) {
				called := false
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					called = true
					wantPath := "/v1/models"
					if apiFormat == "gemini" {
						wantPath = "/v1beta/models"
					}
					if r.URL.Path != wantPath || r.URL.Query().Get("api_key") != "private-query-key" {
						t.Error("model catalog endpoint or query authentication changed")
					}
					for _, header := range []string{"Authorization", "X-Goog-Api-Key"} {
						if _, exists := r.Header[header]; exists {
							t.Errorf("empty API key emitted %s", header)
						}
					}
					if headerOnly && r.Header.Get("X-Gateway-Key") != "private-gateway-header" {
						t.Error("model catalog custom authentication header was dropped")
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"data":[{"id":"custom-model"}],"models":[{"name":"models/custom-model"}]}`)
				}))
				defer server.Close()
				input := ChannelModelsRequest{BaseURL: server.URL + "?api_key=private-query-key", APIFormat: apiFormat}
				if headerOnly {
					input.Headers = []OutboundHeader{{Name: "X-Gateway-Key", Value: "private-gateway-header"}}
				}
				models, err := (&Service{}).FetchChannelModelCatalog(context.Background(), &model.User{ID: "owner"}, input)
				if err != nil || !called || len(models) != 1 || models[0].ID != "custom-model" {
					t.Fatalf("model catalog = %#v, called=%v, err=%v", models, called, err)
				}
			})
		}
	}
}

func TestCustomAPIHeaderOnlyGenerationExecution(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	for _, headerOnly := range []bool{false, true} {
		t.Run(map[bool]string{true: "header_only", false: "no_auth"}[headerOnly], func(t *testing.T) {
			called := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if r.URL.Path != "/v1/chat/completions" || r.URL.Query().Get("api_key") != "private-query-key" {
					t.Error("custom generation endpoint or query authentication changed")
				}
				if r.Header.Get("Authorization") != "" {
					t.Error("empty API key emitted an Authorization header")
				}
				if headerOnly && r.Header.Get("X-Gateway-Key") != "private-gateway-header" {
					t.Error("custom authentication header was dropped")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"custom response"}}]}`)
			}))
			defer server.Close()
			config := providerConfig{BaseURL: server.URL + "/v1?api_key=private-query-key", APIFormat: "openai", InterfaceType: string(model.ChannelInterfaceChatCompletion), Model: "custom-text"}
			if headerOnly {
				config.Headers = []OutboundHeader{{Name: "X-Gateway-Key", Value: "private-gateway-header"}}
			}
			stream := false
			raw, _ := json.Marshal(canvasGenerationInput{Mode: "text", Prompt: "hello", Config: config, TextOptions: canvasTextOptions{Stream: &stream}})
			result, err := (&Service{}).processCanvasGenerationTask(context.Background(), "owner", "", "canvas_text", "hello", string(raw))
			if err != nil || !called || result["text"] != "custom response" {
				t.Fatalf("custom generation = %#v, called=%v, error=%v", result, called, err)
			}
		})
	}
}

func TestCustomAPITransportRejectsRedirects(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	forwarded := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = true
		w.WriteHeader(http.StatusOK)
	}))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	config := providerConfig{BaseURL: origin.URL, APIKey: "private-api-key"}
	ctx := withCustomProviderEndpoint(context.Background(), config)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, origin.URL+"?api_key=private-query-key", strings.NewReader(`{}`))
	applyProviderAuth(req, config)
	req.Header.Set("X-Gateway-Key", "private-gateway-header")
	if _, _, err := doBinary(req); err == nil || forwarded {
		t.Fatalf("custom redirect was followed or accepted: forwarded=%v, err=%v", forwarded, err)
	}
	err := &url.Error{Op: "Post", URL: origin.URL + "?api_key=private-query-key", Err: errors.New("connection refused")}
	assertCustomAPISecretsAbsent(t, safeProviderLogError(err))
}

func TestCustomAPIVideoHeaderOnlyExecutable(t *testing.T) {
	input := map[string]any{"mode": "video", "config": map[string]any{"baseUrl": "https://8.8.8.8/v1", "model": "video-model", "headers": []any{map[string]any{"name": "X-Gateway-Key", "value": "private-gateway-header"}}}}
	if !hasExecutableProviderVideoConfig(input) {
		t.Fatal("header-only video configuration was rejected before execution")
	}
}
