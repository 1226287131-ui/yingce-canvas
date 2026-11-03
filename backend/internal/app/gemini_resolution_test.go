package app

import (
	"context"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/protocol"
)

func TestGeminiResolutionSurvivesLegacyCanvasAdmission(t *testing.T) {
	adapter := imageParameterTestAdapter(t, "google-gemini-image", "gemini-image")
	registry, err := protocol.NewRegistry(adapter)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, quality, resolution, want string
		parameters                      map[string]any
	}{
		{name: "reported canvas high auto", quality: "high", resolution: "auto", want: "4K"},
		{name: "legacy high empty", quality: "high", want: "4K"},
		{name: "legacy medium", quality: "medium", want: "2K"},
		{name: "legacy low", quality: "low", want: "1K"},
		{name: "legacy tier 4k", quality: "4k", want: "4K"},
		{name: "explicit resolution wins", quality: "low", resolution: "4k", want: "4K"},
		{name: "explicit parameters win", quality: "high", parameters: map[string]any{"imageResolution": "2k"}, want: "2K"},
		{name: "auto uses declared default", quality: "auto", resolution: "auto", want: "1K"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile, err := NormalizeModelCapabilityConfigForModel("image", "gemini-image", "Nano Banana Pro", DefaultModelCapabilityConfigForModel("gemini-image", "Nano Banana Pro"), registry)
			if err != nil {
				t.Fatal(err)
			}
			if !profile.Image.Resolution.Supported || profile.Image.Quality.Supported {
				t.Fatal("legacy Gemini resolution was lost during plugin projection")
			}
			config := providerConfig{Model: "Nano Banana Pro", InterfaceType: "gemini-image", Size: "3:4", Quality: tc.quality, ImageResolution: tc.resolution, ImageParameters: tc.parameters}
			if err := applyImageParameterValues(&config, profile.Image); err != nil {
				t.Fatal(err)
			}
			input := canvasGenerationInput{Mode: "image", Config: config}
			if err := validateImageTask(profile.Image, input); err != nil {
				t.Fatal(err)
			}
			spec, err := adapter.BuildCreate(context.Background(), protocol.RequestContext{Request: protocolRequestFromInput(input)})
			if err != nil {
				t.Fatal(err)
			}
			body := spec.Body.(map[string]any)
			image := body["generationConfig"].(map[string]any)["imageConfig"].(map[string]any)
			if image["imageSize"] != tc.want || image["aspectRatio"] != "3:4" {
				t.Fatalf("upstream imageConfig=%#v, want imageSize=%s and aspectRatio=3:4", image, tc.want)
			}
			if config.Quality != "" {
				t.Fatal("legacy quality must not compete with explicit resolution")
			}
		})
	}
}

func TestGeminiResolutionPreservesExplicitRestrictions(t *testing.T) {
	adapter := imageParameterTestAdapter(t, "google-gemini-image", "gemini-image")
	registry, _ := protocol.NewRegistry(adapter)
	profile := DefaultModelCapabilityConfigForModel("gemini-image", "opaque")
	profile.Image.Resolution = ImageResolutionConfig{Supported: true, Values: []string{"2k"}, Default: "2k"}
	profile.Image.Quality = ImageQualityConfig{Default: "auto"}
	profile, err := NormalizeModelCapabilityConfigForModel("image", "gemini-image", "opaque", profile, registry)
	if err != nil {
		t.Fatal(err)
	}
	config := providerConfig{InterfaceType: "gemini-image", Quality: "high"}
	if err := applyImageParameterValues(&config, profile.Image); err == nil {
		t.Fatal("4K request silently fell back to restricted 2K")
	}
	profile.Image.Parameters = []protocol.ImageParameter{{Name: "imageResolution", Type: "string", Binding: "resolution", Values: []any{"1k"}, Default: "1k"}}
	profile.Image.Resolution = ImageResolutionConfig{}
	profile.Image.Quality = ImageQualityConfig{Supported: true, Values: []string{"high"}, Default: "high"}
	normalizeGeminiLegacyImageResolutionConfig("gemini-image", profile.Image)
	if profile.Image.Resolution.Supported {
		t.Fatal("explicit parameter contract was overwritten")
	}
	other := DefaultImageCapabilityConfig("openai-image", "opaque")
	normalizeGeminiLegacyImageResolutionConfig("openai-image", other)
	if !other.Quality.Supported || other.Resolution.Supported {
		t.Fatal("OpenAI quality semantics changed")
	}
}

func TestGeminiResolutionLegacyConfigWithoutRegistry(t *testing.T) {
	profile, err := NormalizeModelCapabilityConfigForModel("image", "gemini-image", "Nano Banana Pro", DefaultModelCapabilityConfigForModel("gemini-image", "Nano Banana Pro"))
	if err != nil {
		t.Fatal(err)
	}
	if !profile.Image.Resolution.Supported || profile.Image.ResolutionUsesQuality || profile.Image.Quality.Supported {
		t.Fatalf("independent resolution was not retained: %#v", profile.Image)
	}
}

func TestGeminiResolutionSystemChannelAdmission(t *testing.T) {
	profile := DefaultModelCapabilityConfigForModel("gemini-image", "Nano Banana Pro")
	svc, _, channel, channelModel := createSystemChannelSelectionFixture(t, "image", model.ChannelInterfaceGeminiImage, profile, []model.ChannelModelPriceTier{
		newSelectionPriceTier("tier-any", `{}`, "provider-image", "fixed_request"),
	})
	resolved, err := svc.resolveSystemChannelModelSelection(map[string]any{
		"mode": "image", "config": map[string]any{
			"channelId": channel.ID, "model": channelModel.ModelKey,
			"size": "3:4", "quality": "high", "imageResolution": "auto",
		},
	}, "canvas_image", "")
	if err != nil {
		t.Fatal(err)
	}
	config := resolved["config"].(map[string]any)
	options := resolved["capabilityOptions"].(map[string]any)
	if config["imageResolution"] != "4k" || options["imageResolution"] != "4k" {
		t.Fatalf("execution and pricing resolution drifted: config=%#v options=%#v", config, options)
	}
}
