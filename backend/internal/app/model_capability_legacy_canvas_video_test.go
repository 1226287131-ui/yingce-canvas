package app

import (
	"encoding/json"
	"testing"

	"yingce/backend/internal/model"
)

func TestLegacyCanvasVideoCapabilityProfiles(t *testing.T) {
	tests := []struct {
		protocol, model, resolution      string
		min, max, images, videos, audios int
	}{
		{"legacy-canvas-video-v1", "video-v1", "720p", 5, 15, 9, 0, 0},
		{"legacy-canvas-video-v2", "video-v2-480P（按秒计费）", "480p", 5, 15, 9, 3, 3},
		{"legacy-canvas-video-v3", "video-v3-480P（按条计费）", "480p", 4, 30, 30, 10, 10},
		{"legacy-canvas-video-grok", "grok-imagine-video", "720p", 1, 15, 100, 0, 0},
		{"legacy-canvas-video-h3", "minimax-h3", "1920x1088", 4, 15, 9, 3, 3},
	}
	for _, test := range tests {
		t.Run(test.protocol, func(t *testing.T) {
			profile := DefaultModelCapabilityConfigForModel(test.protocol, test.model).Video
			if !videoDurationAllowed(profile.Duration, test.min) || !videoDurationAllowed(profile.Duration, test.max) || videoDurationAllowed(profile.Duration, test.min-1) || videoDurationAllowed(profile.Duration, test.max+1) {
				t.Fatalf("duration = %#v", profile.Duration)
			}
			if profile.References.MaxImages != test.images || profile.References.MaxVideos != test.videos || profile.References.MaxAudios != test.audios || profile.DefaultResolution != test.resolution {
				t.Fatalf("profile = %#v", profile)
			}
			if err := validateVideoCapabilityConfig(profile); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLegacyCanvasSystemVideoAdmissionUsesChannelProtocol(t *testing.T) {
	for _, test := range []struct {
		protocol, ratio, quality, want string
		images                         int
	}{
		{"legacy-canvas-video-grok", "16:9", "1080p", "720p", 2},
		{"legacy-canvas-video-h3", "9:16", "1920x1088", "1088x1920", 1},
	} {
		t.Run(test.protocol, func(t *testing.T) {
			svc, db := newChannelModelTestService(t)
			encoded, err := json.Marshal(DefaultModelCapabilityConfigForModel(test.protocol, "upstream-model"))
			if err != nil {
				t.Fatal(err)
			}
			item := model.ChannelModel{ID: "legacy-model", ChannelID: "legacy-channel", ModelKey: "upstream-model", Capability: "video", Protocol: model.ChannelInterfaceType(test.protocol), Enabled: true, CapabilityConfigJSON: string(encoded)}
			if err := db.Create(&item).Error; err != nil {
				t.Fatal(err)
			}
			refs := make([]any, test.images)
			for i := range refs {
				refs[i] = map[string]any{"url": "https://cdn.example/image.png"}
			}
			config := map[string]any{"channelId": item.ChannelID, "model": item.ModelKey, "interfaceType": "untrusted-client-protocol", "size": test.ratio, "vquality": test.quality, "videoSeconds": "5"}
			input := map[string]any{"mode": "video", "prompt": "clip", "config": config, "referenceImages": refs}
			if err := svc.ValidateTaskCapability(input); err != nil {
				t.Fatal(err)
			}
			if config["vquality"] != test.want {
				t.Fatalf("admission quality = %v, want %s", config["vquality"], test.want)
			}
		})
	}
}

func TestLegacyCanvasVideoV2FullProfile(t *testing.T) {
	profile := DefaultModelCapabilityConfigForModel("legacy-canvas-video-v2", "video-v2-满血兜底版").Video
	if videoDurationAllowed(profile.Duration, 5) || !videoDurationAllowed(profile.Duration, 15) || len(profile.Resolutions) != 1 || profile.Resolutions[0] != "720p" || profile.GenerateAudio.Supported || profile.References.PromptMaxChars != 4000 {
		t.Fatalf("full V2 profile = %#v", profile)
	}
}

// Admission and worker recovery must use the same values as billing.
func TestLegacyCanvasVideoAdmissionAndRecovery(t *testing.T) {
	tests := []struct {
		name, protocol, model, ratio, quality, seconds, want string
		images                                               int
	}{
		{"grok multi-image", "legacy-canvas-video-grok", "grok", "16:9", "1080p", "5", "720p", 2},
		{"grok single-image", "legacy-canvas-video-grok", "grok", "16:9", "1080p", "5", "1080p", 1},
		{"H3 portrait", "legacy-canvas-video-h3", "h3", "9:16", "1920x1088", "5", "1088x1920", 0},
		{"H3 premium", "legacy-canvas-video-h3", "h3", "9:16", "4k", "5", "4K", 1},
		{"V3 fixed 30s", "legacy-canvas-video-v3", "video-v3-480P（按条计费）", "16:9", "720p", "30", "480p", 0},
	}
	service := &Service{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			refs := make([]any, test.images)
			for i := range refs {
				refs[i] = map[string]any{"url": "https://cdn.example/image.png"}
			}
			config := map[string]any{"interfaceType": test.protocol, "model": test.model, "size": test.ratio, "vquality": test.quality, "videoSeconds": test.seconds}
			input := map[string]any{"mode": "video", "prompt": "clip", "config": config, "referenceImages": refs}
			if err := service.ValidateTaskCapability(input); err != nil {
				t.Fatal(err)
			}
			if config["vquality"] != test.want {
				t.Fatalf("admission quality = %v, want %s", config["vquality"], test.want)
			}
			var recovered canvasGenerationInput
			data, _ := json.Marshal(input)
			if err := json.Unmarshal(data, &recovered); err != nil {
				t.Fatal(err)
			}
			if err := service.validateResolvedVideoCapability(&recovered); err != nil {
				t.Fatal(err)
			}
			if recovered.Config.VQuality != test.want {
				t.Fatalf("worker quality = %s", recovered.Config.VQuality)
			}
		})
	}
}

func TestLegacyCanvasH3AdmissionRejectsInvalidInputs(t *testing.T) {
	for _, test := range []struct {
		quality        string
		videos, audios int
	}{{"2K", 0, 0}, {"4K", 0, 0}, {"1920x1088", 1, 0}, {"1920x1088", 0, 1}, {"invalid-size", 0, 0}} {
		input := canvasGenerationInput{}
		input.Config.InterfaceType = "legacy-canvas-video-h3"
		input.Config.VQuality = test.quality
		input.Config.Size = "16:9"
		input.Config.VideoSeconds = "5"
		input.ReferenceVideos = make([]providerMedia, test.videos)
		input.ReferenceAudios = make([]providerMedia, test.audios)
		profile := DefaultModelCapabilityConfigForModel(input.Config.InterfaceType, "h3").Video
		applyFixedVideoResolution(&input, profile)
		if err := validateVideoTask(profile, input); err == nil {
			t.Fatalf("accepted invalid H3 input: %#v", test)
		}
	}
}
