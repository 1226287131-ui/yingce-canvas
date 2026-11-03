package app

import (
	"context"
	"encoding/json"
	"testing"

	"yingce/backend/internal/protocol"
)

func TestOpenAIImageDefaultRatioWithAutoQuality(t *testing.T) {
	adapter := imageParameterTestAdapter(t, "openai-images", "openai-image")
	registry, err := protocol.NewRegistry(adapter)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gpt-image-2-cf", "gpt-image-2.5-sunburst-test", "opaque-image-alias"} {
		for _, saved := range []string{"new", "legacy", "parameterized"} {
			for ratio, want := range map[string]string{"1:1": "1024x1024", "16:9": "1824x1024", "2048x2048": "2048x2048"} {
				t.Run(name+"/"+saved+"/"+ratio, func(t *testing.T) {
					profile := DefaultModelCapabilityConfigForModel("openai-image", name)
					if saved != "new" {
						profile.Image.Size.DefaultTier = ""
					}
					if saved == "parameterized" {
						if err := resolveImageCapabilityParameters(profile.Image, adapter.Metadata().ImageParameters, name); err != nil {
							t.Fatal(err)
						}
					}
					profile, err := NormalizeModelCapabilityConfigForModel("image", "openai-image", name, profile, registry)
					if err != nil {
						t.Fatal(err)
					}
					config := providerConfig{Model: name, InterfaceType: "openai-image", Size: ratio, Quality: "auto"}
					if err := applyImageParameterValues(&config, profile.Image); err != nil {
						t.Fatal(err)
					}
					if config.Size != want || config.Quality != "auto" {
						t.Fatalf("size=%q quality=%q, want %q and auto", config.Size, config.Quality, want)
					}
					request := protocolRequestFromInput(canvasGenerationInput{Mode: "image", Config: config})
					spec, err := adapter.BuildCreate(context.Background(), protocol.RequestContext{Request: request})
					if err != nil {
						t.Fatal(err)
					}
					encoded, err := json.Marshal(spec.Body)
					if err != nil {
						t.Fatal(err)
					}
					var body map[string]any
					if err := json.Unmarshal(encoded, &body); err != nil {
						t.Fatal(err)
					}
					if body["size"] != want || (body["quality"] != nil && body["quality"] != "auto") {
						t.Fatalf("upstream body = %#v", body)
					}
				})
			}
		}
	}
}

func TestOpenAIImageDefaultSizeTierPreservesExplicitAndCustomMappings(t *testing.T) {
	for _, name := range []string{"explicit-tier", "custom-pixels", "custom-groups", "explicit-resolution", "other-protocol"} {
		t.Run(name, func(t *testing.T) {
			profile := DefaultImageCapabilityConfig("openai-image", "opaque")
			profile.Size.DefaultTier = ""
			protocolID, want := "openai-image", ""
			switch name {
			case "explicit-tier":
				profile.Size.DefaultTier, want = "2k", "2k"
			case "custom-pixels":
				profile.Size.Presets[0].Width = 1080
				profile.Size.Presets[0].Height = 1080
				profile.Size.Presets[0].Size = "1080x1080"
			case "custom-groups":
				profile.Size.Presets = profile.Size.Presets[1:]
			case "explicit-resolution":
				disabled := false
				profile.Parameters = []protocol.ImageParameter{{Name: "imageResolution", Default: "2k", Enabled: &disabled}}
			case "other-protocol":
				protocolID = "opaque-provider"
			}
			normalizeOpenAIImageDefaultSizeTier(protocolID, profile)
			if profile.Size.DefaultTier != want {
				t.Fatalf("default tier=%q, want %q", profile.Size.DefaultTier, want)
			}
		})
	}
}
