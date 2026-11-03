import { expect, test } from "bun:test";
import { defaultModelCapabilityConfig, modelCapabilityConfigFor } from "../src/lib/model-capabilities";
import { LEGACY_CANVAS_H3_SIZES, legacyCanvasH3Resolution } from "../src/lib/legacy-canvas-video";
import { backendProviderConfig } from "../src/services/api/generation-task";
import { createModelChannel, defaultConfig, encodeChannelModel } from "../src/stores/use-config-store";

test("legacy video SKU contracts override stale saved profiles", () => {
    for (const [protocol, model, resolution, seconds] of [
        ["legacy-canvas-video-v2", "video-v2-满血兜底版", "720p", 15],
        ["legacy-canvas-video-v2", "video-v2-480P（按秒计费）", "480p", 5],
        ["legacy-canvas-video-v3", "video-v3-480P（按条计费）", "480p", 4],
        ["legacy-canvas-video-v3", "video-v3-720P（按条计费）", "720p", 4],
    ] as const) {
        const saved = defaultModelCapabilityConfig("openai-video", model);
        saved.video!.resolutions = ["1080p"];
        saved.video!.duration.default = 6;
        const profile = modelCapabilityConfigFor({ channels: [{ id: "personal", models: [model], modelCosts: [{ model, protocol, capabilityConfig: saved }] }] }, `personal::${model}`).video!;
        expect(profile.resolutions).toEqual([resolution]);
        expect(profile.duration.default).toBe(seconds);
        if (model.includes("满血")) {
            expect(profile.generateAudio.supported).toBe(false);
            expect(profile.references.promptMaxChars).toBe(4000);
        }
    }
});

test("H3 ratio changes preserve each pixel tier and premium resolutions", () => {
    for (const sizes of Object.values(LEGACY_CANVAS_H3_SIZES)) {
        for (const [ratio, target] of Object.entries(LEGACY_CANVAS_H3_SIZES)) {
            sizes.forEach((size, tier) => expect(legacyCanvasH3Resolution(ratio, size)).toBe(target[tier]));
        }
    }
    expect(legacyCanvasH3Resolution("9:16", "4k")).toBe("4K");
    expect(legacyCanvasH3Resolution("9:16", "invalid")).toBe("invalid");
});

test("personal H3 task uses matching pixels and preserves the exact upstream model and connection", () => {
    const protocol = "legacy-canvas-video-h3";
    const modelName = "MiniMax-H3-custom-sku";
    const channel = createModelChannel({ id: "personal", name: "Personal", baseUrl: "https://example.com/gateway/v1?region=test", apiKey: "synthetic-key", models: [modelName], modelCosts: [{ model: modelName, capability: "video", protocol, billingMode: "fixed_request", unitPriceMicrocredits: 0 }] });
    const model = encodeChannelModel(channel.id, modelName);
    const config = { ...defaultConfig, channels: [channel], model, videoModel: model, size: "9:16", vquality: "1920x1088" };
    const request = backendProviderConfig(config, "video");
    expect(request).toMatchObject({ model: modelName, interfaceType: protocol, baseUrl: channel.baseUrl, apiKey: "synthetic-key", size: "9:16", vquality: "1088x1920" });
    expect(backendProviderConfig({ ...config, size: "auto", vquality: "auto" }, "video").vquality).toBe("1920x1088");
});

test("reference capabilities expose audio only for protocols that accept it", () => {
    for (const suffix of ["v1", "v2", "v3", "grok", "h3"]) {
        const profile = defaultModelCapabilityConfig(`legacy-canvas-video-${suffix}`, "custom").video!;
        expect(profile.references.maxAudios > 0).toBe(["v2", "v3", "h3"].includes(suffix));
        expect(profile.watermark.supported).toBe(false);
    }
});
