import type { VideoCapabilityConfig } from "./model-capabilities";

export const LEGACY_CANVAS_VIDEO_PROTOCOLS = ["legacy-canvas-video-v1", "legacy-canvas-video-v2", "legacy-canvas-video-v3", "legacy-canvas-video-grok", "legacy-canvas-video-h3"];

export const LEGACY_CANVAS_H3_SIZES: Record<string, string[]> = {
    "1:1": ["640x640", "1024x1024", "1440x1440"],
    "2:3": ["544x800", "832x1248", "1184x1760"],
    "3:2": ["800x544", "1248x832", "1760x1184"],
    "3:4": ["576x736", "896x1184", "1248x1664"],
    "4:3": ["736x576", "1184x896", "1664x1248"],
    "9:16": ["480x864", "768x1376", "1088x1920"],
    "16:9": ["864x480", "1376x768", "1920x1088"],
    "21:9": ["992x416", "1568x672", "2208x960"],
};

export function legacyCanvasH3Resolution(ratio: string, resolution: string) {
    if (["2K", "4K"].includes(resolution.toUpperCase())) return resolution.toUpperCase();
    const tier = Object.values(LEGACY_CANVAS_H3_SIZES).map((sizes) => sizes.indexOf(resolution)).find((index) => index >= 0);
    return tier === undefined ? resolution : LEGACY_CANVAS_H3_SIZES[ratio]?.[tier] || resolution;
}

// These are request contracts of the old canvas, including full model SKU names.
// Apply them to both new defaults and saved profiles to keep UI and billing aligned.
export function legacyCanvasVideoCapability(video: VideoCapabilityConfig, protocol?: string, model = ""): VideoCapabilityConfig {
    if (!LEGACY_CANVAS_VIDEO_PROTOCOLS.includes(protocol || "")) return video;
    const profile: VideoCapabilityConfig = { ...video, references: { ...video.references }, operations: ["text_to_video", "image_to_video"], defaultOperation: "text_to_video", watermark: { supported: false, default: false } };
    const name = model.trim().toLowerCase().split("::").at(-1)!.replace(/^models\//, "");
    const references = (images: number, videos: number, audios: number, imageMB: number, videoMB = 0, audioMB = 0) => {
        profile.references = { promptMaxChars: 8000, minImages: 0, maxImages: images, maxVideos: videos, maxAudios: audios, maxImageBytes: imageMB * 1024 * 1024, maxVideoBytes: videoMB * 1024 * 1024, maxAudioBytes: audioMB * 1024 * 1024, maxVideoDurationSeconds: 0, maxAudioDurationSeconds: 0 };
        if (videos) profile.operations.push("reference_to_video");
        if (audios) profile.operations.push("audio_to_video");
    };
    const fixed = (resolution: string) => { profile.resolutions = [resolution]; profile.defaultResolution = resolution; };
    profile.generateAudio = { supported: false, default: false };
    profile.defaultRatio = "16:9";
    switch (protocol) {
        case "legacy-canvas-video-v1":
            references(9, 0, 0, 12);
            profile.duration = { selection: "enum", values: [5, 10, 15], default: 5 };
            profile.ratios = ["16:9", "9:16", "1:1", "4:3", "3:4"];
            fixed("720p");
            break;
        case "legacy-canvas-video-v2":
            references(9, 3, 3, 12, 48, 16);
            profile.duration = { selection: "range", min: 5, max: 15, step: 1, default: 5 };
            profile.ratios = ["21:9", "16:9", "4:3", "1:1", "3:4", "9:16"];
            profile.resolutions = ["480p", "720p", "1080p"];
            profile.defaultResolution = "720p";
            profile.generateAudio = { supported: true, default: true };
            if (name === "video-v2-满血兜底版") {
                profile.references.promptMaxChars = 4000;
                profile.duration = { selection: "enum", values: [15], default: 15 };
                profile.ratios = ["16:9", "9:16"];
                profile.generateAudio = { supported: false, default: false };
                fixed("720p");
            } else if (name === "video-v2-720p（按秒计费）") fixed("720p");
            else if (["video-v2-480p（按秒计费）", "video-v2-480p（按条计费）"].includes(name)) fixed("480p");
            break;
        case "legacy-canvas-video-v3":
            references(30, 10, 10, 20);
            profile.duration = { selection: "range", min: 4, max: 30, step: 1, default: 4 };
            profile.ratios = ["16:9", "1:1", "9:16", "21:9"];
            profile.resolutions = ["480p", "720p"];
            profile.defaultResolution = "480p";
            profile.generateAudio = { supported: true, default: true };
            if (["video-v3-480p", "video-v3-480p（按条计费）"].includes(name)) fixed("480p");
            else if (name === "video-v3-720p（按条计费）") fixed("720p");
            break;
        case "legacy-canvas-video-grok":
            references(100, 0, 0, 0);
            profile.duration = { selection: "range", min: 1, max: 15, step: 1, default: 5 };
            profile.ratios = ["16:9", "9:16", "1:1", "4:3", "3:4", "2:3", "3:2"];
            profile.resolutions = ["480p", "720p", "1080p"];
            profile.defaultResolution = "720p";
            break;
        case "legacy-canvas-video-h3":
            references(9, 3, 3, 64, 64, 64);
            profile.duration = { selection: "range", min: 4, max: 15, step: 1, default: 5 };
            profile.ratios = Object.keys(LEGACY_CANVAS_H3_SIZES);
            profile.resolutions = [...Object.values(LEGACY_CANVAS_H3_SIZES).flat(), "2K", "4K"];
            profile.defaultResolution = "1920x1088";
            break;
    }
    return profile;
}
