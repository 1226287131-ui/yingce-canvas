package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

func agentCustomTestRequest(baseURL string) CloudAgentRequest {
	req := agentTestRequest()
	req.ChannelID, req.ChannelModelKey = "", ""
	req.Model, req.CustomModelID = "customer-text", "customer::customer-text"
	profile := DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceChatCompletion), req.Model)
	profile.Text.ContextWindowTokens, profile.Text.MaxOutputTokens = 32_000, 4_000
	profile.Text.References.MaxImages = 2
	req.CustomModels = []CloudAgentCustomModel{{
		ID: req.CustomModelID, Name: "客户文本模型", Capability: "text",
		Config: map[string]any{
			"baseUrl": baseURL, "apiKey": "customer-test-key", "model": req.Model,
			"interfaceType": string(model.ChannelInterfaceChatCompletion), "apiFormat": "openai",
			"headers":          []OutboundHeader{{Name: "X-Gateway-Token", Value: "customer-header-secret"}},
			"capabilityConfig": profile,
		},
	}}
	return req
}

func assertAgentCustomSecretsAbsent(t *testing.T, value any, secrets ...string) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	var check func(any, string)
	check = func(current any, path string) {
		switch typed := current.(type) {
		case string:
			for _, secret := range secrets {
				if strings.Contains(typed, secret) {
					t.Fatalf("custom credential leaked at %s", path)
				}
			}
		case map[string]any:
			for key, child := range typed {
				check(child, path+"."+key)
			}
		case []any:
			for index, child := range typed {
				check(child, fmt.Sprintf("%s[%d]", path, index))
			}
		}
	}
	check(document, "$")
}

func TestCloudAgentCustomSnapshotIsProtectedAndRecoverable(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	s, db, _, _ := creationTestService(t)
	s.legacyCloudAgentRootTask = false
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	req := agentCustomTestRequest("http://127.0.0.1:12345/v1?token=private-address-token")
	run, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil || duplicate.ID != run.ID {
		t.Fatalf("encrypted snapshot broke idempotency: %v", err)
	}
	if req.CustomModels[0].Config["apiKey"] != "customer-test-key" {
		t.Fatal("admission mutated the caller's snapshot")
	}
	task, err := s.repo.TaskForUser("user", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	execution, state := decodeCloudAgentCompactionState(t, s, run.ID)
	for _, persisted := range []string{task.InputJSON, execution.StateJSON} {
		assertAgentCustomSecretsAbsent(t, persisted, "customer-test-key", "customer-header-secret", "private-address-token", "http://127.0.0.1")
	}
	assertAgentCustomSecretsAbsent(t, run, encryptedSettingPrefix, "customer-test-key", "customer-header-secret", "private-address-token")
	assertAgentCustomSecretsAbsent(t, state.Canonical, encryptedSettingPrefix, "customer-test-key", "customer-header-secret", "private-address-token")
	if err := validateCloudAgentRequest(&state.Request, true); err != nil {
		t.Fatalf("durable snapshot cannot recover: %v", err)
	}
	if err := validateCloudAgentRequest(&state.Request); err == nil {
		t.Fatal("browser input may not reuse encrypted snapshots")
	}
	config := cloudAgentModelConfig(state.Request)
	if err := s.decryptTaskSecrets(config); err != nil {
		t.Fatal(err)
	}
	if config["apiKey"] != "customer-test-key" || config["baseUrl"] != req.CustomModels[0].Config["baseUrl"] {
		t.Fatal("recovered credentials differ from admitted snapshot")
	}
	budget := s.cloudAgentContextBudgetForRequest(state.Request)
	if budget.Source != "custom-model" || budget.ContextWindowTokens != 32_000 || budget.MaxOutputTokens != 4_000 || !s.cloudAgentVisionEnabled(state.Request) {
		t.Fatalf("custom context/vision capability was ignored: %+v", budget)
	}
	var orders int64
	if err := db.Model(&model.BillingOrder{}).Where("task_id = ?", run.ID).Count(&orders).Error; err != nil || orders != 0 {
		t.Fatalf("custom root created a managed billing order: count=%d err=%v", orders, err)
	}
}

func TestCloudAgentCustomSnapshotRejectsInvalidSelections(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	for _, test := range []struct {
		name   string
		change func(*CloudAgentRequest)
	}{
		{"missing selection", func(req *CloudAgentRequest) { req.CustomModelID = "another-user::model" }},
		{"duplicate ID", func(req *CloudAgentRequest) { req.CustomModels = append(req.CustomModels, req.CustomModels[0]) }},
		{"model mismatch", func(req *CloudAgentRequest) { req.Model = "not-authorized" }},
		{"mixed managed route", func(req *CloudAgentRequest) { req.ChannelID = "channel" }},
		{"non-text primary", func(req *CloudAgentRequest) { req.CustomModels[0].Capability = "image" }},
		{"private address", func(req *CloudAgentRequest) {
			req.CustomModels[0].Config["baseUrl"] = "http://169.254.169.254/latest/meta-data"
		}},
		{"encrypted header input", func(req *CloudAgentRequest) {
			req.CustomModels[0].Config["headers"] = []OutboundHeader{{Name: "X-Token", Value: encryptedSettingPrefix + "opaque"}}
		}},
		{"unsafe header", func(req *CloudAgentRequest) {
			req.CustomModels[0].Config["headers"] = []OutboundHeader{{Name: "Host", Value: "another-host"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := agentCustomTestRequest("http://127.0.0.1:12345")
			test.change(&req)
			if err := validateCloudAgentRequest(&req); err == nil {
				t.Fatal("invalid custom snapshot accepted")
			}
		})
	}
	for _, apiKey := range []string{"", "system"} {
		req := agentCustomTestRequest("http://127.0.0.1:12345")
		req.CustomModels[0].Config["apiKey"] = apiKey
		if err := validateCloudAgentRequest(&req); err != nil {
			t.Fatalf("header-only or literal API key rejected: %v", err)
		}
		s, _, _, _ := creationTestService(t)
		if err := s.protectCloudAgentCustomModels(&req); err != nil {
			t.Fatal(err)
		}
		if err := validateCloudAgentRequest(&req, true); err != nil {
			t.Fatalf("valid protected snapshot rejected: %v", err)
		}
	}
}

func TestCloudAgentCustomSnapshotHonorsFeatureGate(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	s, _, _, _ := creationTestService(t)
	if _, err := s.UpdateFeatureAvailability(&model.User{ID: "admin", Role: model.UserRoleAdmin}, FeatureAvailability{CustomChannelsEnabled: false}); err != nil {
		t.Fatal(err)
	}
	req := agentCustomTestRequest("http://127.0.0.1:12345")
	if _, err := s.CreateCloudAgentRun("user", req, ""); err == nil {
		t.Fatal("disabled custom channels accepted an Agent run")
	}
	if _, err := s.UpdateUserAgentMemorySetting("user", AgentMemorySettingRequest{CustomModelID: req.CustomModelID, CustomModels: req.CustomModels}); err == nil {
		t.Fatal("disabled custom channels accepted a scheduled custom model")
	}
}

func TestAgentMemoryCustomModelSurvivesReloadAndQueuesProtectedTask(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	s, _, _, _ := creationTestService(t)
	req := agentCustomTestRequest("http://127.0.0.1:12345")
	view, err := s.UpdateUserAgentMemorySetting("user", AgentMemorySettingRequest{CompactInterval: "daily", CustomModelID: req.CustomModelID, CustomModels: req.CustomModels, Model: req.Model})
	if err != nil {
		t.Fatal(err)
	}
	if view.CustomModelID != req.CustomModelID || view.ChannelID != "" || view.Model != req.Model {
		t.Fatalf("custom compact selection lost: %+v", view)
	}
	setting, err := s.repo.AgentMemorySetting("user")
	if err != nil {
		t.Fatal(err)
	}
	assertAgentCustomSecretsAbsent(t, setting.CustomModelJSON, "customer-test-key", "customer-header-secret", "http://127.0.0.1")
	assertAgentCustomSecretsAbsent(t, view, encryptedSettingPrefix, "customer-test-key", "customer-header-secret", "http://127.0.0.1")
	if _, err := s.CreateUserAgentMemory("user", AgentMemoryRequest{Topic: "model.custom", Category: "model", Situation: "使用自定义模型", Lesson: "保持用户选择的模型"}); err != nil {
		t.Fatal(err)
	}
	if err := s.startUserAgentMemoryCompact(setting, true); err != nil {
		t.Fatalf("scheduled custom model failed after reload: %v", err)
	}
	task, err := s.repo.TaskForUser("user", setting.CompactTaskID)
	if err != nil {
		t.Fatal(err)
	}
	assertAgentCustomSecretsAbsent(t, task.InputJSON, "customer-test-key", "customer-header-secret", "http://127.0.0.1")
	plain, err := s.decryptTaskInputJSON(task.InputJSON)
	if err != nil || !strings.Contains(plain, "customer-test-key") || !strings.Contains(plain, "customer-header-secret") {
		t.Fatalf("scheduled task lost custom credentials: %v", err)
	}
	if _, err := s.UpdateUserAgentMemorySetting("user", AgentMemorySettingRequest{CompactInterval: "off", ChannelID: "channel", ChannelModelKey: "text-test", Model: "text-test"}); err != nil {
		t.Fatal(err)
	}
	setting, _ = s.repo.AgentMemorySetting("user")
	if setting.CustomModelJSON != "" {
		t.Fatal("switching to a managed model retained the custom snapshot")
	}
}

func TestCloudAgentCustomCompactionReusesProtectedModel(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	s, db, _, _ := creationTestService(t)
	s.legacyCloudAgentRootTask = false
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	root, err := s.CreateCloudAgentRun("user", agentCustomTestRequest("http://127.0.0.1:12345"), "")
	if err != nil {
		t.Fatal(err)
	}
	run, state := decodeCloudAgentCompactionState(t, s, root.ID)
	state.ActiveTaskID = ""
	state.ContextCompaction = &cloudAgentContextCompaction{Status: "requested", Resume: true, TurnCount: 1}
	run = saveCloudAgentCompactionState(t, s, run, &state)
	stepBefore := state.Step
	if err := s.enqueueCloudAgentContextCompaction(run, &state); err != nil {
		t.Fatal(err)
	}
	_, state = decodeCloudAgentCompactionState(t, s, run.ID)
	if state.ContextCompaction == nil || state.ContextCompaction.Status != "running" || state.Step != stepBefore {
		t.Fatal("custom compaction did not checkpoint independently of model steps")
	}
	task, err := s.repo.TaskForUser("user", state.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	assertAgentCustomSecretsAbsent(t, task.InputJSON, "customer-test-key", "customer-header-secret", "http://127.0.0.1")
	plain, err := s.decryptTaskInputJSON(task.InputJSON)
	if err != nil {
		t.Fatal(err)
	}
	var input canvasGenerationInput
	if err := json.Unmarshal([]byte(plain), &input); err != nil || input.Config.APIKey != "customer-test-key" || input.Config.Model != "customer-text" || input.Config.ChannelID != "" {
		t.Fatalf("context compaction lost the selected custom model: %v", err)
	}
	if task.Operation != cloudAgentContextCompactionOperation || input.Config.SystemPrompt == "" {
		t.Fatal("context compaction lost its dedicated provider contract")
	}
}

func TestCloudAgentCustomMediaApprovalUsesFrozenSnapshot(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	s, db, args := agentMediaFixture(t)
	req := agentCustomTestRequest("http://127.0.0.1:12345")
	req.PermissionMode = "request_approval"
	req.Budget.MaxGenerationTasks, req.Budget.MaxVideoSeconds = 2, 24
	video := CloudAgentCustomModel{ID: "customer::seedance-test", Name: "客户视频模型", Capability: "video", Config: map[string]any{
		"baseUrl": "http://127.0.0.1:12345", "apiKey": "customer-video-key", "model": "seedance-test", "interfaceType": string(model.ChannelInterfaceVolcengineArkVideo),
		"capabilityConfig": DefaultModelCapabilityConfigForModel(string(model.ChannelInterfaceVolcengineArkVideo), "seedance-test"),
		"defaultOptions":   map[string]any{"watermark": false},
	}}
	req.CustomModels = append(req.CustomModels, video)
	args.ChannelID, args.ChannelModelKey = cloudAgentCustomChannelPrefix+video.ID, "seedance-test"
	root, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	run, state := decodeCloudAgentCompactionState(t, s, root.ID)
	state.ActiveTaskID, state.Calls = "", []cloudAgentCall{agentMediaCall(args)}
	run = saveCloudAgentCompactionState(t, s, run, &state)
	catalog, err := s.cloudAgentModelList(nil, state.Request)
	if err != nil {
		t.Fatal(err)
	}
	assertAgentCustomSecretsAbsent(t, catalog, "customer-test-key", "customer-video-key", "customer-header-secret", "http://127.0.0.1", encryptedSettingPrefix)
	if err := s.advanceCloudAgentTool(run, &state); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.CloudAgentRun("user", run.ID)
	if err != nil || waiting.Approval == nil {
		t.Fatalf("custom video approval missing: %v", err)
	}
	assertAgentCustomSecretsAbsent(t, waiting, "customer-test-key", "customer-video-key", "http://127.0.0.1", encryptedSettingPrefix)
	// Re-read the durable checkpoint before approving, as a restarted backend does.
	run, state = decodeCloudAgentCompactionState(t, s, root.ID)
	assertAgentCustomSecretsAbsent(t, run.StateJSON, "customer-video-key", "http://127.0.0.1")
	if err := s.DecideCloudAgentApproval("user", run.ID, waiting.Approval.ID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.advanceCloudAgentByID("user", run.ID); err != nil {
		t.Fatal(err)
	}
	_, state = decodeCloudAgentCompactionState(t, s, root.ID)
	task, err := s.repo.TaskForUser("user", state.MediaTaskID)
	if err != nil {
		t.Fatal(err)
	}
	assertAgentCustomSecretsAbsent(t, task.InputJSON, "customer-video-key", "http://127.0.0.1")
	plain, err := s.decryptTaskInputJSON(task.InputJSON)
	if err != nil {
		t.Fatal(err)
	}
	var input canvasGenerationInput
	if err := json.Unmarshal([]byte(plain), &input); err != nil || input.Config.APIKey != "customer-video-key" || input.Config.ChannelID != "" || input.Config.VideoSeconds != "12" {
		t.Fatalf("approval did not preserve the frozen custom model: %v", err)
	}
	var orders int64
	if err := db.Model(&model.BillingOrder{}).Where("task_id = ?", task.ID).Count(&orders).Error; err != nil || orders != 0 {
		t.Fatalf("custom media created a managed billing order: count=%d err=%v", orders, err)
	}
	bad := args
	bad.ChannelID = cloudAgentCustomChannelPrefix + "unapproved::model"
	if _, _, err := s.prepareCloudAgentMedia(run, &state, agentMediaCall(bad)); err == nil {
		t.Fatal("media tool accepted a custom model outside its run snapshot")
	}
}

func TestCloudAgentCustomRuntimeToolsAndContinuation(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	if _, err := agentRuntimeDirForTest(); err != nil {
		t.Skip(err.Error())
	}
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	t.Setenv("REDIS_URL", "")
	var mu sync.Mutex
	var bodies []map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer customer-test-key" || r.Header.Get("X-Gateway-Token") != "customer-header-secret" {
			t.Error("worker did not decrypt the customer's credentials")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		assertAgentCustomSecretsAbsent(t, body, "customer-test-key", "customer-header-secret", encryptedSettingPrefix, "http://127.0.0.1")
		if body["model"] != "customer-text" {
			t.Errorf("wrong upstream model: %v", body["model"])
		}
		mu.Lock()
		bodies = append(bodies, body)
		call := len(bodies)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if call == 1 {
			tool := firstUpstreamToolName(body)
			if tool == "" {
				t.Error("custom model received no tools")
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"custom-call","type":"function","function":{"name":"` + tool + `","arguments":"{}"}}]}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"客户模型已检查画布"}}]}`))
	}))
	defer upstream.Close()
	s, db, _, _ := creationTestService(t)
	s = New(s.repo, s.dataDir)
	t.Cleanup(func() { _ = s.Close() })
	if err := db.Create(&model.CanvasProject{ID: "agent-canvas", UserID: "user", PayloadJSON: `{"nodes":[]}`}).Error; err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = s.ProcessNextTask()
			time.Sleep(20 * time.Millisecond)
		}
	}()
	req := agentCustomTestRequest(upstream.URL)
	run, err := s.CreateCloudAgentRun("user", req, "")
	if err != nil {
		t.Fatal(err)
	}
	final := waitCloudAgentStatus(t, s, run.ID, "completed")
	assertAgentCustomSecretsAbsent(t, final, "customer-test-key", "customer-header-secret", encryptedSettingPrefix, upstream.URL)
	req.Prompt, req.IdempotencyKey = "继续总结画布", "customer-continuation-key"
	next, err := s.CreateCloudAgentRun("user", req, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = waitCloudAgentStatus(t, s, next.ID, "completed")
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 3 {
		t.Fatalf("expected tool round-trip and one continuation, got %d upstream calls", len(bodies))
	}
	raw, _ := json.Marshal(bodies[1]["messages"])
	if !strings.Contains(string(raw), `"tool_call_id":"custom-call"`) {
		t.Fatal("custom model lost the tool result")
	}
	raw, _ = json.Marshal(bodies[2]["messages"])
	if !strings.Contains(string(raw), "客户模型已检查画布") {
		t.Fatal("custom model lost trusted continuation history")
	}
}
