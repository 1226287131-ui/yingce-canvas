package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestRunningHubURLsKeepGatewayPathAndFixedQuery(t *testing.T) {
	for _, test := range []struct {
		name string
		base string
		root string
	}{
		{name: "official default", root: "https://www.runninghub.cn"},
		{name: "official version", base: "https://www.runninghub.cn/openapi/v2/", root: "https://www.runninghub.cn"},
		{name: "gateway version and query", base: "https://gateway.example/rh/openapi/v2/?api_key=a%2Fb%2Bc&region=cn#ignored", root: "https://gateway.example/rh?api_key=a%2Fb%2Bc&region=cn"},
		{name: "escaped gateway path", base: "https://gateway.example/rh%2Ftenant/openapi?api_key=a%2Fb%2Bc&region=cn", root: "https://gateway.example/rh%2Ftenant?api_key=a%2Fb%2Bc&region=cn"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := runningHubRootURL(test.base)
			if root != test.root {
				t.Fatalf("root = %q, want %q", root, test.root)
			}
			joined, err := url.Parse(runningHubURL(root, "/task/openapi/outputs?after=2"))
			if err != nil {
				t.Fatal(err)
			}
			base, _ := url.Parse(test.root)
			if joined.EscapedPath() != base.EscapedPath()+"/task/openapi/outputs" || joined.Query().Get("after") != "2" || joined.Fragment != "" {
				t.Fatalf("incorrect protocol URL: %s", joined.Redacted())
			}
			if base.Query().Get("api_key") != joined.Query().Get("api_key") || base.Query().Get("region") != joined.Query().Get("region") {
				t.Fatal("fixed gateway query changed")
			}
		})
	}
	root := runningHubRootURL("https://gateway.example/rh/openapi/v2?api_key=gateway-key")
	if got := resolveRunningHubOutputURL(root, "/output/image.png?signature=result-key"); got != "https://gateway.example/rh/output/image.png?api_key=gateway-key&signature=result-key" {
		t.Fatalf("relative output URL = %q", got)
	}
	if got := resolveRunningHubOutputURL(root, "https://cdn.example/image.png?signature=result-key"); got != "https://cdn.example/image.png?signature=result-key" {
		t.Fatal("absolute output ownership or query changed")
	}
}

func TestRunningHubFixedQueryAcrossManagementUploadSubmitAndPoll(t *testing.T) {
	allowLoopbackProviderTest(t)
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++
		if r.URL.Query().Get("api_key") != "gateway/key+value" || r.URL.Query().Get("region") != "cn" {
			t.Error("gateway query credentials changed or lost")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/gateway/output/result.png" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("image"))
			return
		}
		if r.URL.Path == "/gateway/task/openapi/upload" {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			if r.FormValue("apiKey") != "upload-key" {
				t.Error("upload credential changed")
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"fileName":"input/image.png"}}`))
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["apiKey"] != "credit-key" {
			t.Error("management or task credential changed")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/gateway/api/openapi/getJsonApiFormat":
			_, _ = w.Write([]byte(`{"code":0,"data":{"prompt":{}}}`))
		case "/gateway/api/webapp/apiCallDemo":
			_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
		case "/gateway/task/openapi/create", "/gateway/task/openapi/ai-app/run":
			_, _ = w.Write([]byte(`{"code":0,"data":{"taskId":"provider-task"}}`))
		case "/gateway/task/openapi/outputs":
			_, _ = w.Write([]byte(`{"code":0,"data":[{"fileUrl":"output/result.png"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	baseURL := server.URL + "/gateway/openapi/v2/?api_key=gateway%2Fkey%2Bvalue&region=cn"
	config := providerConfig{BaseURL: baseURL, APIKey: "credit-key", RunningHubUploadKey: "upload-key", WorkflowID: "workflow-1"}
	service := &Service{}
	ctx := context.Background()
	request := RunningHubWorkflowFetchRequest{BaseURL: baseURL, APIKey: config.APIKey, WorkflowID: config.WorkflowID, WebappID: "app-1"}
	if _, err := service.FetchRunningHubWorkflowInfo(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := service.FetchRunningHubAppInfo(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := service.uploadRunningHubMedia(ctx, runningHubRootURL(baseURL), config, providerMedia{DataURL: "data:image/png;base64,aW1hZ2U="}); err != nil {
		t.Fatal(err)
	}
	for _, appID := range []string{"", "app-1"} {
		config.WebappID = appID
		if _, err := service.runRunningHubWorkflow(ctx, canvasGenerationInput{Mode: "image", Prompt: "test", Config: config}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.pollRunningHubVideoWorkflowWithPolicy(ctx, config, runningHubRootURL(baseURL), "provider-task", fastVideoPollPolicy()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/openapi/getJsonApiFormat", "/api/webapp/apiCallDemo", "/task/openapi/upload", "/task/openapi/create", "/task/openapi/ai-app/run", "/task/openapi/outputs", "/output/result.png"} {
		if requests["/gateway"+path] == 0 {
			t.Errorf("real request did not reach %s", path)
		}
	}
}
