package app

import "strings"

var legacyCanvasH3Sizes = map[string][]string{
	"1:1":  {"640x640", "1024x1024", "1440x1440"},
	"2:3":  {"544x800", "832x1248", "1184x1760"},
	"3:2":  {"800x544", "1248x832", "1760x1184"},
	"3:4":  {"576x736", "896x1184", "1248x1664"},
	"4:3":  {"736x576", "1184x896", "1664x1248"},
	"9:16": {"480x864", "768x1376", "1088x1920"},
	"16:9": {"864x480", "1376x768", "1920x1088"},
	"21:9": {"992x416", "1568x672", "2208x960"},
}

// Normalize before admission/billing and again on worker recovery. H3 pixel
// presets encode their ratio; Grok's multi-image mode only supports up to 720p.
func applyLegacyCanvasVideoSelection(input *canvasGenerationInput, profile *VideoCapabilityConfig) {
	protocol := strings.TrimSpace(input.Config.InterfaceType)
	if !strings.HasPrefix(protocol, "legacy-canvas-video-") {
		return
	}
	resolution := videoResolutionNameRequest(profile, input.Config.VQuality)
	if resolution == "" && isAutomaticVideoResolution(input.Config.VQuality) {
		resolution = profile.DefaultResolution
	}
	if protocol == "legacy-canvas-video-grok" && len(input.ReferenceImages) > 1 && strings.EqualFold(resolution, "1080p") {
		input.Config.VQuality = "720p"
	}
	if protocol != "legacy-canvas-video-h3" {
		return
	}
	ratio := input.Config.Size
	if ratio == "" || ratio == "auto" {
		ratio = profile.DefaultRatio
	}
	input.Config.Size = ratio
	for _, sizes := range legacyCanvasH3Sizes {
		for tier, size := range sizes {
			if resolution == size && len(legacyCanvasH3Sizes[ratio]) > tier {
				input.Config.VQuality = legacyCanvasH3Sizes[ratio][tier]
				return
			}
		}
	}
	if strings.EqualFold(resolution, "2K") || strings.EqualFold(resolution, "4K") {
		input.Config.VQuality = strings.ToUpper(resolution)
	}
}

func validateLegacyCanvasVideoReferences(input canvasGenerationInput) error {
	if input.Config.InterfaceType != "legacy-canvas-video-h3" || len(input.ReferenceImages) > 0 {
		return nil
	}
	if strings.EqualFold(input.Config.VQuality, "2K") || strings.EqualFold(input.Config.VQuality, "4K") {
		return newModelCapabilityArgumentError("referenceNodeIds", "invalid_combination", BadAuthRequest("MiniMax H3 的 2K/4K 生成至少需要 1 张参考图"))
	}
	if len(input.ReferenceVideos) > 0 || len(input.ReferenceAudios) > 0 {
		return newModelCapabilityArgumentError("referenceNodeIds", "invalid_combination", BadAuthRequest("MiniMax H3 的参考视频或参考音频必须同时携带至少 1 张参考图"))
	}
	return nil
}
