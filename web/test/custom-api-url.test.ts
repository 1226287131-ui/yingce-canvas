import { afterEach, describe, expect, test } from "bun:test";
import axios from "axios";

import { appendProviderApiPath } from "../src/lib/provider-api-url";
import type { RegisteredPlugin } from "../src/lib/plugins/plugin-types";
import { requestAudioGeneration } from "../src/services/api/audio";
import { requestImageQuestion } from "../src/services/api/image";
import { fetchChannelModels } from "../src/services/api/image-models";
import { geminiApiUrl } from "../src/services/api/image-transport";
import { createAgnesVideoTask, pollAgnesVideoTask } from "../src/services/api/video-provider-agnes";
import { createGeminiVeoTask, pollGeminiVeoTask } from "../src/services/api/video-provider-gemini";
import { createMiniMaxVideoTask, pollMiniMaxVideoTask } from "../src/services/api/video-provider-minimax";
import { videoResponseTools } from "../src/services/api/video-response";
import { createVideoTransport } from "../src/services/api/video-transport";
import { createPluginHostContext } from "../src/services/plugin-host";
import { buildApiUrl, createModelChannel, defaultConfig, resolveModelRequestConfig, type ModelChannel } from "../src/stores/use-config-store";

const originalAxiosRequest = axios.request;
const originalAxiosGet = axios.get;
const originalFetch = globalThis.fetch;
afterEach(() => {
    axios.request = originalAxiosRequest;
    axios.get = originalAxiosGet;
    globalThis.fetch = originalFetch;
});

const fixedQuery = "api_key=synthetic-query-secret&token=a%2Fb%2Bc%3D&space=hello%20world&tag=one&tag=two";

function configFor(input?: Partial<ModelChannel>) {
    const channel = createModelChannel({ id: "personal", name: "Personal API", baseUrl: `https://provider.example/v1/?${fixedQuery}#ignored`, apiKey: "", models: ["custom-model"], ...input });
    return { ...defaultConfig, channels: [channel], model: "personal::custom-model", textModel: "personal::custom-model", videoSeconds: "6", size: "16:9", vquality: "720" };
}

function relayUpstream(url: unknown, requestHeaders: HeadersInit | undefined, pathname: string) {
    expect(String(url)).toEndWith("/ai/custom");
    expect(String(url)).not.toContain("synthetic-query-secret");
    expect(String(url)).not.toContain("provider.example");
    const headers = new Headers(requestHeaders);
    const upstream = new URL(headers.get("X-Canvas-Upstream-URL")!);
    expect(upstream.pathname).toBe(pathname);
    expect(upstream.searchParams.get("api_key")).toBe("synthetic-query-secret");
    expect(upstream.searchParams.get("token")).toBe("a/b+c=");
    expect(upstream.searchParams.getAll("tag")).toEqual(["one", "two"]);
    expect(upstream.hash).toBe("");
    expect(headers.has("Authorization")).toBe(false);
    expect(headers.has("x-goog-api-key")).toBe(false);
    return upstream;
}

describe("custom API URL composition", () => {
    test("appends endpoint paths before fixed query credentials without changing their encoding", () => {
        expect(buildApiUrl(`https://provider.example/v1/?${fixedQuery}#ignored`, "/chat/completions")).toBe(`https://provider.example/v1/chat/completions?${fixedQuery}`);
        expect(buildApiUrl(`https://provider.example/?${fixedQuery}`, "/models")).toBe(`https://provider.example/v1/models?${fixedQuery}`);
        expect(buildApiUrl("https://provider.example/v1", "/models")).toBe("https://provider.example/v1/models");
    });

    test("replaces the API version and merges endpoint query parameters", () => {
        expect(buildApiUrl(`https://provider.example/gateway/v1/?${fixedQuery}&alt=json`, "/v1beta/models?alt=sse")).toBe(`https://provider.example/gateway/v1beta/models?${fixedQuery}&alt=sse`);
        expect(buildApiUrl("https://provider.example/v1tenant/gateway/v1?key=x", "/v1beta/models")).toBe("https://provider.example/v1tenant/gateway/v1beta/models?key=x");
        expect(buildApiUrl("https://provider.example/v1tenant/gateway/v1/chat/completions?key=x", "/v1beta/models")).toBe("https://provider.example/v1tenant/gateway/v1beta/models?key=x");
        expect(appendProviderApiPath(`https://provider.example/v1?${fixedQuery}`, "?alt=sse")).toBe(`https://provider.example/v1?${fixedQuery}&alt=sse`);
    });

    test("keeps Ark plan normalization and relative system proxy paths", () => {
        expect(buildApiUrl(`https://provider.example/api/plan/v3/images/generations?${fixedQuery}`, "/images/generations")).toBe(`https://provider.example/api/plan/v3/images/generations?${fixedQuery}`);
        expect(buildApiUrl("https://provider.example/api/plan/v3/images/generations?token=ends-with/", "/images/generations")).toBe("https://provider.example/api/plan/v3/images/generations?token=ends-with/");
        expect(buildApiUrl("/api/ai/system/test-channel", "/chat/completions")).toBe("/api/ai/system/test-channel/chat/completions");
        expect(geminiApiUrl({ baseUrl: "/api/ai/system/test-channel", model: "custom-model" }, "streamGenerateContent")).toBe("/api/ai/system/test-channel/models/custom-model:streamGenerateContent?alt=sse");
    });

    test("uses the actual model directory and audio relay paths with query credentials", async () => {
        const calls: Record<string, unknown>[] = [];
        const audio = new Blob(["synthetic audio"], { type: "audio/mpeg" });
        axios.request = (async (input: Record<string, unknown>) => {
            calls.push(input);
            return { data: calls.length === 1 ? { data: [{ id: "custom-model" }] } : audio };
        }) as typeof axios.request;
        const config = configFor();
        expect((await fetchChannelModels(config.channels[0])).models).toEqual(["custom-model"]);
        expect(await requestAudioGeneration(config, "test prompt")).toBe(audio);
        relayUpstream(calls[0].url, calls[0].headers as HeadersInit, "/v1/models");
        relayUpstream(calls[1].url, calls[1].headers as HeadersInit, "/v1/audio/speech");
        expect(calls.every((call) => call.withCredentials === true)).toBe(true);
    });

    test("streams Gemini text with both fixed query credentials and alt=sse through the relay", async () => {
        let requestUrl: unknown;
        let requestOptions: RequestInit | undefined;
        globalThis.fetch = (async (url: unknown, options?: RequestInit) => {
            requestUrl = url;
            requestOptions = options;
            return new Response('data: {"candidates":[{"content":{"parts":[{"text":"OK"}]}}]}\n\n', { headers: { "Content-Type": "text/event-stream" } });
        }) as typeof fetch;
        const config = configFor({ baseUrl: `https://provider.example/v1beta/?${fixedQuery}&alt=json`, apiFormat: "gemini" });
        expect(await requestImageQuestion(config, [{ role: "user", content: "test prompt" }], () => undefined)).toBe("OK");
        const upstream = relayUpstream(requestUrl, requestOptions?.headers, "/v1beta/models/custom-model:streamGenerateContent");
        expect(upstream.search).toBe(`?${fixedQuery}&alt=sse`);
        expect(requestOptions?.credentials).toBe("include");
    });

    test("plugin text services use the selected custom channel and fixed query credentials", async () => {
        let requestUrl: unknown;
        let requestOptions: RequestInit | undefined;
        globalThis.fetch = (async (url: unknown, options?: RequestInit) => {
            requestUrl = url;
            requestOptions = options;
            return Response.json({ choices: [{ message: { content: "OK" } }] });
        }) as typeof fetch;
        const plugin: RegisteredPlugin = { manifest: { apiVersion: "yingce.plugin/v1", id: "custom-api-test", name: "Custom API Test", version: "0.0.1", description: "Test", permissions: ["ai.text"], contributes: {} } };
        const context = createPluginHostContext(plugin, { manifest: plugin.manifest, enabled: true, config: {}, installedAt: "", updatedAt: "" }, configFor({ interfaceType: "chat-completion" }));
        expect((await context.services.ai!.text!.requestToolResponse({ messages: [{ role: "user", content: "test prompt" }] })).content).toBe("OK");
        relayUpstream(requestUrl, requestOptions?.headers, "/v1/chat/completions");
        expect(JSON.parse(String(requestOptions?.body)).model).toBe("custom-model");
    });

    test("Veo, MiniMax, and Agnes create and poll through the relay without losing fixed query credentials", async () => {
        const calls: Record<string, unknown>[] = [];
        axios.request = (async (input: Record<string, unknown>) => {
            calls.push(input);
            return { data: { name: "operations/veo-1", task_id: "minimax-1", video_id: "agnes-1", status: "queued" } };
        }) as typeof axios.request;
        const providers = [
            { interfaceType: "gemini-veo", basePath: "/v1beta", create: createGeminiVeoTask, poll: pollGeminiVeoTask, createPath: "/v1beta/models/custom-model:predictLongRunning", pollPath: "/v1beta/operations/veo-1" },
            { interfaceType: "minimax-video", basePath: "/v2", create: createMiniMaxVideoTask, poll: pollMiniMaxVideoTask, createPath: "/v2/video_generation", pollPath: "/v2/query/video_generation/minimax-1" },
            { interfaceType: "agnes-video", basePath: "/v1", create: createAgnesVideoTask, poll: pollAgnesVideoTask, createPath: "/v1/videos", pollPath: "/agnesapi" },
        ] as const;
        for (const provider of providers) {
            const config = configFor({ baseUrl: `https://provider.example${provider.basePath}/?${fixedQuery}`, interfaceType: provider.interfaceType });
            const resolved = resolveModelRequestConfig(config, config.model);
            const deps = { transport: createVideoTransport(resolved), response: videoResponseTools };
            const task = await provider.create(deps, resolved, provider.interfaceType === "agnes-video" ? "agnes-video-2.5" : "custom-model", "test prompt", [], [], []);
            expect((await provider.poll(deps, resolved, task)).status).toBe("pending");
            const [created, polled] = calls.slice(-2);
            relayUpstream(created.url, created.headers as HeadersInit, provider.createPath);
            const upstream = relayUpstream(polled.url, polled.headers as HeadersInit, provider.pollPath);
            if (provider.interfaceType === "agnes-video") {
                expect(upstream.searchParams.get("video_id")).toBe("agnes-1");
                expect(upstream.searchParams.get("model_name")).toBe("agnes-video-2.5");
            }
        }
    });

    test("does not copy gateway query credentials onto a foreign video result URL", async () => {
        const config = configFor({ baseUrl: `https://provider.example/v1beta?${fixedQuery}`, interfaceType: "gemini-veo" });
        const resolved = resolveModelRequestConfig(config, config.model);
        const resultUrl = "https://media.example/result.mp4?signed=result-only";
        const video = new Blob(["synthetic video"], { type: "video/mp4" });
        let downloadedUrl = "";
        axios.request = (async () => ({ data: { done: true, response: { uri: resultUrl } } })) as typeof axios.request;
        axios.get = (async (url: string) => { downloadedUrl = url; return { data: video }; }) as typeof axios.get;
        const state = await pollGeminiVeoTask({ transport: createVideoTransport(resolved), response: videoResponseTools }, resolved, { id: "operations/veo-1", provider: "gemini-veo", model: config.model });
        expect(state.status).toBe("completed");
        expect(downloadedUrl).toBe(resultUrl);
        expect(downloadedUrl).not.toContain("synthetic-query-secret");
    });
});
