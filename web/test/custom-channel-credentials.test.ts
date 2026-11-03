import { afterEach, describe, expect, test } from "bun:test";
import axios from "axios";

import { channelValidationError } from "../src/pages/settings/channel-settings-pane";
import { requestAudioGeneration } from "../src/services/api/audio";
import { channelRequest } from "../src/services/api/custom-channel-relay";
import { fetchChannelModels } from "../src/services/api/image-models";
import { createModelChannel, defaultConfig, normalizeConfigSnapshot, useConfigStore } from "../src/stores/use-config-store";

const originalAxiosRequest = axios.request;
afterEach(() => { axios.request = originalAxiosRequest; });

function headerOnlyChannel() {
    return createModelChannel({
        id: "personal", name: "Personal API", baseUrl: "https://provider.example/v1", apiKey: "", apiFormat: "openai", models: ["custom-model"],
        headers: [{ name: "X-Gateway-Token", value: "synthetic-header-secret" }],
    });
}

describe("custom channel header-only credentials", () => {
    test("accepts ordinary channels and still validates URLs, sensitive headers, and AK/SK protocols", () => {
        const channel = headerOnlyChannel();
        expect(channelValidationError(channel)).toBe("");
        expect(channelValidationError({ ...channel, baseUrl: "" })).toContain("Base URL");
        expect(channelValidationError({ ...channel, headers: [{ name: "Cookie", value: "private-cookie" }] })).not.toBe("");
        expect(channelValidationError({ ...channel, interfaceType: "volcengine-jimeng-image" })).toContain("Access Key");
        expect(channelValidationError({ ...channel, apiKey: "access-key", interfaceType: "volcengine-jimeng-image" })).toContain("Secret Key");
    });

    test("keeps header-only defaults during configuration normalization and considers them ready", () => {
        const channel = { ...headerOnlyChannel(), id: "default", name: "默认渠道", baseUrl: defaultConfig.baseUrl, models: ["gpt-4o"] };
        const config = normalizeConfigSnapshot({ config: { ...defaultConfig, channels: [channel], model: "default::gpt-4o" } }).config;
        expect(config.channels).toHaveLength(1);
        expect(config.channels[0].headers).toEqual(channel.headers);
        expect(useConfigStore.getState().isAiConfigReady(config, config.model)).toBe(true);
        expect(useConfigStore.getState().isAiConfigReady({ ...config, channels: [{ ...channel, baseUrl: "" }] }, config.model)).toBe(false);
    });

    test("removes stale authorization and forwards only the current channel headers", () => {
        const channel = headerOnlyChannel();
        const request = channelRequest(channel, "https://provider.example/v1/models", { Authorization: "Bearer stale-key", "x-goog-api-key": "stale-gemini-key", "X-Canvas-Upstream-Headers": "stale-headers" });
        const headers = new Headers(request.headers);
        expect(headers.has("Authorization")).toBe(false);
        expect(headers.has("x-goog-api-key")).toBe(false);
        expect(JSON.parse(atob(headers.get("X-Canvas-Upstream-Headers")!))).toEqual(channel.headers);
        expect(request.credentials).toBe("include");
        expect(request.url).toEndWith("/ai/custom");
        expect(channelRequest({ ...channel, apiKey: " current-key " }, "https://provider.example/v1/models").headers.authorization).toBe("Bearer current-key");
    });

    test("preserves custom headers when fetching the directory through the shared transport", async () => {
        let options: Record<string, unknown> = {};
        axios.request = (async (input: Record<string, unknown>) => {
            options = input;
            return { data: { data: [{ id: "custom-model" }] } };
        }) as typeof axios.request;
        const channel = headerOnlyChannel();
        const result = await fetchChannelModels(channel);
        expect(result.models).toEqual(["custom-model"]);
        const headers = new Headers(options.headers as Record<string, string>);
        expect(headers.has("Authorization")).toBe(false);
        expect(JSON.parse(atob(headers.get("X-Canvas-Upstream-Headers")!))).toEqual(channel.headers);
        expect(headers.get("X-Canvas-Upstream-URL")).toBe("https://provider.example/v1/models");
    });

    test("generates audio with the selected channel headers and no API Key", async () => {
        const channel = headerOnlyChannel();
        const audio = new Blob(["synthetic audio"], { type: "audio/mpeg" });
        let options: Record<string, unknown> = {};
        axios.request = (async (input: Record<string, unknown>) => { options = input; return { data: audio }; }) as typeof axios.request;
        const config = { ...defaultConfig, channels: [channel], model: "personal::custom-model" };
        expect(await requestAudioGeneration(config, "test prompt")).toBe(audio);
        const headers = new Headers(options.headers as Record<string, string>);
        expect(headers.has("Authorization")).toBe(false);
        expect(JSON.parse(atob(headers.get("X-Canvas-Upstream-Headers")!))).toEqual(channel.headers);
        expect(headers.get("X-Canvas-Upstream-URL")).toBe("https://provider.example/v1/audio/speech");
        expect(options.data).toMatchObject({ model: "custom-model", input: "test prompt" });
    });
});
