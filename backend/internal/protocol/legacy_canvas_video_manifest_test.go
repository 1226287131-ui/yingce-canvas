package protocol

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyCanvasVideoFullV2OmitsUnsupportedFields(t *testing.T) {
	adapter := legacyCanvasVideoAdapter(t, "legacy-canvas-video-v2")
	spec, err := adapter.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{Model: "video-v2-满血兜底版", Duration: 15, AspectRatio: "16:9", Resolution: "720p", GenerateAudio: true}})
	if err != nil {
		t.Fatal(err)
	}
	body := manifestTestBody(t, spec)
	for _, key := range []string{"resolution", "generate_audio"} {
		if _, ok := body[key]; ok {
			t.Fatalf("full V2 unexpectedly sends %s", key)
		}
	}
	for _, key := range []string{"images", "videos", "audios"} {
		if _, ok := body[key]; !ok {
			t.Fatalf("full V2 missing %s", key)
		}
	}
}

func TestLegacyCanvasVideoResponsesAndContent(t *testing.T) {
	for _, id := range []string{"legacy-canvas-video-v1", "legacy-canvas-video-v2", "legacy-canvas-video-v3", "legacy-canvas-video-grok", "legacy-canvas-video-h3"} {
		t.Run(id, func(t *testing.T) {
			adapter := legacyCanvasVideoAdapter(t, id)
			for _, payload := range []string{
				`{"code":400,"message":"invalid request"}`,
				`{"data":{"code":"bad","msg":"rejected"}}`,
				`{"error":"upstream failed"}`,
				`{"data":{"error":{"message":"upstream failed"}}}`,
				`{"status":"failed","message":"failed"}`,
			} {
				result, err := adapter.ParseCreate(context.Background(), []byte(payload))
				if err != nil || result.Status != StatusFailed || result.Message == "" {
					t.Fatalf("failure %s = %#v, err = %v", payload, result, err)
				}
			}
			for _, payload := range []string{
				`{"code":0,"data":{"id":"task-1","status":"completed","video_url":"https://cdn.example/clip.mp4"}}`,
				`{"code":"success","data":{"data":{"task_id":"task-1","status":"completed","content":{"video_url":"https://cdn.example/clip.mp4"}}}}`,
				`{"code":200,"taskId":"task-1","status":"COMPLETED","metadata":{"direct_url":"https://cdn.example/clip.mp4"}}`,
			} {
				result, err := adapter.ParseCreate(context.Background(), []byte(payload))
				if err != nil || result.Status != StatusSucceeded || result.TaskID != "task-1" || result.Result == nil || len(result.Result.Videos) != 1 || !result.Result.Videos[0].Ephemeral {
					t.Fatalf("success %s = %#v, err = %v", payload, result, err)
				}
			}
			pending, err := adapter.ParseCreate(context.Background(), []byte(`{"error":{},"data":{"id":"task-1","status":"queued"}}`))
			if err != nil || pending.Status != StatusPending || pending.TaskID != "task-1" {
				t.Fatalf("queued = %#v, err = %v", pending, err)
			}
			done, err := adapter.ParseCreate(context.Background(), []byte(`{"id":"task-1","status":"completed"}`))
			waitingForURL := id == "legacy-canvas-video-v1" || id == "legacy-canvas-video-v3"
			if err != nil || waitingForURL && done.Status != StatusPending || !waitingForURL && done.Status != StatusSucceeded {
				t.Fatalf("completion without URL = %#v, err = %v", done, err)
			}
			spec, resultErr := adapter.(ResultAdapter).BuildResult(context.Background(), PollContext{TaskID: "task-1"})
			if waitingForURL {
				if resultErr == nil {
					t.Fatal("unexpected content fallback")
				}
			} else if resultErr != nil || spec.Path != "/videos/task-1/content" {
				t.Fatalf("content = %#v: %v", spec, resultErr)
			}
		})
	}
}

func legacyCanvasVideoAdapter(t *testing.T, id string) Adapter {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "plugin-packages", "legacy-canvas-video", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	adapters, err := LoadInstalledProviders(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, adapter := range adapters {
		if adapter.Metadata().ID == id {
			return adapter
		}
	}
	t.Fatalf("missing legacy Canvas adapter %q", id)
	return nil
}

func TestLegacyCanvasVideoManifestRequestProfiles(t *testing.T) {
	ctx := context.Background()
	media := []MediaReference{{URL: "https://cdn.example/one.png", Name: "one.png", MIMEType: "image/png", Order: 1}, {URL: "https://cdn.example/two.png", Name: "two.png", MIMEType: "image/png", Order: 2}}

	v1 := legacyCanvasVideoAdapter(t, "legacy-canvas-video-v1")
	v1Spec, err := v1.BuildCreate(ctx, RequestContext{Request: GenerationRequest{Model: "video-v1", Prompt: "@参考图2 对着 @Image1", Duration: 5, AspectRatio: "16:9", Images: media}})
	if err != nil {
		t.Fatal(err)
	}
	v1Body := manifestTestBody(t, v1Spec)
	if v1Spec.Path != "/video/generations" || manifestInt(v1Body["duration"]) != 5 || v1Body["prompt"] == "@参考图2 对着 @Image1" {
		t.Fatalf("V1 request = %#v, body = %#v", v1Spec, v1Body)
	}

	v2 := legacyCanvasVideoAdapter(t, "legacy-canvas-video-v2")
	v2Spec, err := v2.BuildCreate(ctx, RequestContext{Request: GenerationRequest{Model: "video-v2-480P（按秒计费）", Prompt: "@图片1 配合 @参考视频1 和 @音频1", Duration: 6, AspectRatio: "9:16", Resolution: "480p", Images: media[:1], Videos: []MediaReference{{URL: "https://cdn.example/motion.mp4"}}, Audios: []MediaReference{{URL: "https://cdn.example/voice.mp3"}}}})
	if err != nil {
		t.Fatal(err)
	}
	v2Body := manifestTestBody(t, v2Spec)
	if v2Spec.Path != "/videos" || v2Body["prompt"] != "@Image1 配合 @Video1 和 @Audio1" || v2Body["resolution"] != "480p" {
		t.Fatalf("V2 request = %#v, body = %#v", v2Spec, v2Body)
	}

	v3 := legacyCanvasVideoAdapter(t, "legacy-canvas-video-v3")
	for _, duration := range []int{29, 30} {
		spec, err := v3.BuildCreate(ctx, RequestContext{Request: GenerationRequest{Model: "video-v3-480P（按条计费）", Prompt: "clip", Duration: duration, AspectRatio: "16:9", Resolution: "480p"}})
		if err != nil {
			t.Fatal(err)
		}
		body := manifestTestBody(t, spec)
		wantPath, ratioKey := "/videos/generations", "aspect_ratio"
		if duration == 30 {
			wantPath, ratioKey = "/videos", "ratio"
		}
		audioKey, absentKey := "audio", "generate_audio"
		if duration == 30 {
			audioKey, absentKey = "generate_audio", "audio"
		}
		if _, ok := body[absentKey]; ok {
			t.Fatalf("V3 %d unexpected %s", duration, absentKey)
		}
		if _, ok := body[audioKey]; !ok {
			t.Fatalf("V3 %d missing %s", duration, audioKey)
		}
		poll, err := v3.BuildPoll(ctx, PollContext{TaskID: "task-1", Request: GenerationRequest{Duration: duration}})
		if err != nil || poll.Path != wantPath+"/task-1" {
			t.Fatalf("V3 poll = %#v, err = %v", poll, err)
		}
		if spec.Path != wantPath || body[ratioKey] != "16:9" || body["resolution"] != "480p" {
			t.Fatalf("V3 %d seconds = %#v, body = %#v", duration, spec, body)
		}
	}

	grok := legacyCanvasVideoAdapter(t, "legacy-canvas-video-grok")
	grokSpec, err := grok.BuildCreate(ctx, RequestContext{Request: GenerationRequest{Model: "grok-imagine-video", Prompt: "clip", Duration: 5, Images: media}})
	if err != nil {
		t.Fatal(err)
	}
	if grokSpec.ContentType != "multipart/form-data" || len(grokSpec.Files) != 2 || grokSpec.Files[0].Name != "input_reference" || grokSpec.Files[0].Filename != "one.png" {
		t.Fatalf("Grok multipart request = %#v", grokSpec)
	}

	h3 := legacyCanvasVideoAdapter(t, "legacy-canvas-video-h3")
	h3Spec, err := h3.BuildCreate(ctx, RequestContext{Request: GenerationRequest{Model: "minimax-h3", Prompt: "clip", Duration: 5, AspectRatio: "9:16", Resolution: "4K", Images: media[:1]}})
	if err != nil {
		t.Fatal(err)
	}
	h3Body := manifestTestBody(t, h3Spec)
	if h3Body["workflow_id"] != "cf-multi-reference" || h3Body["size"] != "4K" || h3Body["aspect_ratio"] != "9:16" {
		t.Fatalf("H3 request = %#v", h3Body)
	}
}

func TestLegacyCanvasH3ManifestRejectsInvalidReferenceCombinations(t *testing.T) {
	adapter := legacyCanvasVideoAdapter(t, "legacy-canvas-video-h3")
	_, err := adapter.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{Model: "minimax-h3", Prompt: "clip", Duration: 5, Resolution: "2K"}})
	if err == nil {
		t.Fatal("H3 2K without an image was accepted")
	}
	_, err = adapter.BuildCreate(context.Background(), RequestContext{Request: GenerationRequest{Model: "minimax-h3", Prompt: "clip", Duration: 5, Resolution: "1920x1088", Videos: []MediaReference{{URL: "https://cdn.example/motion.mp4"}}}})
	if err == nil {
		t.Fatal("H3 video reference without an image was accepted")
	}
}
