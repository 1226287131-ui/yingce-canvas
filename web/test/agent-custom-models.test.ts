import { describe, expect, it } from "bun:test";
import { agentCustomModels, agentMemoryModelSelection, agentModelSelection, agentRequestFingerprint, restoreAgentRequestModels } from "../src/services/agent-model-config";
import { backendProviderConfig } from "../src/services/api/generation-task";
import type { CreateAgentRunInput } from "../src/services/api/agent";
import { createModelChannel, defaultConfig, encodeChannelModel } from "../src/stores/use-config-store";
import { defaultModelCapabilityConfig } from "../src/lib/model-capabilities";

function fixture() {
    const channels = (["text", "image", "video", "audio"] as const).map((capability) => createModelChannel({
        id: `personal-${capability}`, name: `My ${capability}`, baseUrl: "https://provider.example/v1", apiKey: `key-${capability}`,
        secretKey: "secret", headers: [{ name: "X-Customer", value: "customer-secret" }], interfaceType: `${capability}-protocol`, models: [`my-${capability}`],
        modelCosts: [{ model: `my-${capability}`, capability, billingMode: "fixed_request", unitPriceMicrocredits: 0, defaultOptions: { watermark: false } }],
    }));
    channels.push(createModelChannel({ id: "disabled", enabled: false, interfaceType: "text-protocol", models: ["hidden"] }));
    channels.push(createModelChannel({ id: "system", scope: "system", interfaceType: "text-protocol", models: ["managed"], modelCosts: [{ model: "managed", capability: "text", billingMode: "fixed_request", unitPriceMicrocredits: 0 }] }));
    return { ...defaultConfig, channels, model: encodeChannelModel(channels[0].id, channels[0].models[0]) };
}

describe("user API credentials across Agent and ordinary task admission", () => {
    for (const [apiFormat, interfaceType] of [["openai", "chat-completion"], ["claude", "claude-api"], ["gemini", "gemini-generate-content"]] as const) {
        it(`includes manually added ${apiFormat} text models in the admitted catalog`, async () => {
            const channel = createModelChannel({
                id: "manual", name: "Manual", apiFormat, baseUrl: "https://provider.example/v1", apiKey: "customer-key",
                headers: [{ name: "X-Customer", value: "customer-header" }], models: ["gpt-5.6-sol"],
            });
            const config = { ...defaultConfig, channels: [channel], model: "manual::gpt-5.6-sol" };
            const snapshot = agentCustomModels(config);
            expect(channel.interfaceType).toBeUndefined();
            expect(snapshot).toHaveLength(1);
            expect(agentModelSelection(config, config.model)).toEqual({ customModelId: snapshot[0].id });
            expect(snapshot[0].config).toMatchObject({ interfaceType, apiFormat, model: "gpt-5.6-sol", apiKey: "customer-key", headers: channel.headers });
            expect(agentMemoryModelSelection(config, config.model).customModels).toEqual(snapshot);
            const fingerprint = await agentRequestFingerprint(snapshot);
            const request: CreateAgentRunInput = { canvasId: "canvas", prompt: "你好", customModelId: snapshot[0].id, idempotencyKey: "manual-model-retry" };
            expect((await restoreAgentRequestModels(request, fingerprint, config)).customModels).toEqual(snapshot);
        });
    }

    it("preserves explicit channel and per-model protocols instead of inferring them", () => {
        const channel = createModelChannel({
            id: "explicit", name: "Explicit", apiFormat: "claude", interfaceType: "openai-response",
            baseUrl: "https://provider.example/v1", apiKey: "customer-key", headers: [{ name: "X-Customer", value: "customer-header" }],
            models: ["responses-model", "plugin-model"],
            modelCosts: [{ model: "plugin-model", protocol: "customer-text-plugin", capability: "text", billingMode: "fixed_request", unitPriceMicrocredits: 0 }],
        });
        const snapshot = agentCustomModels({ ...defaultConfig, channels: [channel] });
        expect(snapshot.map((entry) => entry.config.interfaceType)).toEqual(["openai-response", "customer-text-plugin"]);
        expect(snapshot.every((entry) => JSON.stringify(entry.config.headers) === JSON.stringify(channel.headers))).toBe(true);
    });

    it("rejects a selection omitted from the catalog and does not infer media protocols", () => {
        const channel = createModelChannel({ id: "manual", models: ["text-model", "image-model"] });
        const config = { ...defaultConfig, channels: [{ ...channel, baseUrl: "" }] };
        expect(agentCustomModels(config)).toEqual([]);
        expect(() => agentModelSelection(config, "manual::text-model")).toThrow("缺少上游地址或请求协议");
        const withAddress = { ...config, channels: [{ ...channel, baseUrl: "https://provider.example/v1" }] };
        expect(agentCustomModels(withAddress).map((entry) => entry.id)).toEqual(["manual::text-model"]);
        expect(() => agentModelSelection(withAddress, "manual::image-model")).toThrow("缺少上游地址或请求协议");
    });

    it("submits the raw model name and only the selected text credentials for memory compression", () => {
        const config = fixture();
        const request = agentMemoryModelSelection(config, config.model);
        expect(request.customModelId).toBe(config.model);
        expect(request.customModels).toHaveLength(1);
        expect(request.customModels?.[0].capability).toBe("text");
        expect(request.model).toBe(request.customModels?.[0].config.model);
        expect(request.model).toBe("my-text");
        expect(agentMemoryModelSelection(config, "system::managed")).toEqual({ channelId: "system", channelModelKey: "managed", model: "managed" });
        expect(agentMemoryModelSelection(config, "")).toEqual({});
    });

    it("keeps all four capabilities and custom headers, excludes disabled/system credentials", () => {
        const config = fixture();
        const snapshot = agentCustomModels(config);
        expect(snapshot.map((model) => model.capability)).toEqual(["text", "image", "video", "audio"]);
        for (const model of snapshot) {
            expect(model.config.apiKey).toBe(`key-${model.capability}`);
            expect(model.config.secretKey).toBe("secret");
            expect(model.config.headers).toEqual([{ name: "X-Customer", value: "customer-secret" }]);
            expect(model.config.defaultOptions).toEqual({ watermark: false });
            expect(model.id).not.toContain("https://");
            const direct = backendProviderConfig({ ...config, model: model.id }, model.capability);
            expect(direct.headers).toEqual(model.config.headers);
        }
        expect(agentModelSelection(config, config.model)).toEqual({ customModelId: snapshot[0].id });
        expect(agentModelSelection(config, "system::managed")).toEqual({ channelId: "system", channelModelKey: "managed" });
        expect(() => agentModelSelection(config, "disabled::hidden")).toThrow("停用");
    });

    it("rehydrates a pending snapshot only when its full configuration still matches", async () => {
        const config = fixture();
        const snapshot = agentCustomModels(config);
        const fingerprint = await agentRequestFingerprint(snapshot);
        expect(fingerprint).toMatch(/^[a-f0-9]{64}$/);
        const request: CreateAgentRunInput = { canvasId: "canvas", prompt: "继续", customModelId: snapshot[0].id, idempotencyKey: "stable-retry-key" };
        expect((await restoreAgentRequestModels(request, fingerprint, config)).customModels).toEqual(snapshot);
        const changed = { ...config, channels: config.channels.map((channel, i) => i === 0 ? { ...channel, apiKey: "changed-key" } : channel) };
        await expect(restoreAgentRequestModels(request, fingerprint, changed)).rejects.toThrow("配置已更改");
        expect(await restoreAgentRequestModels(request, undefined, changed)).toBe(request);
    });

    it("keeps capabilities bound to the selected channel when model names overlap", () => {
        const first = defaultModelCapabilityConfig("chat-completion", "shared-model");
        const second = defaultModelCapabilityConfig("chat-completion", "shared-model");
        first.text!.references.maxImages = 0;
        second.text!.references.maxImages = 5;
        const channels = [first, second].map((capabilityConfig, index) => createModelChannel({
            id: `channel-${index}`, name: `Channel ${index}`, baseUrl: "https://provider.example/v1", apiKey: "test-key", interfaceType: "chat-completion", models: ["shared-model"],
            modelCosts: [{ model: "shared-model", capability: "text", billingMode: "fixed_request", unitPriceMicrocredits: 0, capabilityConfig }],
        }));
        const config = { ...defaultConfig, channels, model: "channel-1::shared-model" };
        expect(backendProviderConfig(config, "text").capabilityConfig.text?.references.maxImages).toBe(5);
        expect((agentCustomModels(config).find((entry) => entry.id === config.model)?.config.capabilityConfig as typeof second).text?.references.maxImages).toBe(5);
    });
});
