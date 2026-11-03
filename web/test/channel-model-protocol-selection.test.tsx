import { afterAll, afterEach, expect, test } from "bun:test";
import { Window } from "happy-dom";
import type { ModelChannel } from "../src/stores/use-config-store";

const dom = new Window({ url: "http://localhost/" });
const originals = new Map<string, PropertyDescriptor | undefined>();
for (const [key, value] of Object.entries({
    window: dom,
    document: dom.document,
    navigator: dom.navigator,
    HTMLElement: dom.HTMLElement,
    Element: dom.Element,
    Node: dom.Node,
    SVGElement: dom.SVGElement,
    ShadowRoot: dom.ShadowRoot,
    MutationObserver: dom.MutationObserver,
    ResizeObserver: dom.ResizeObserver,
    getComputedStyle: dom.getComputedStyle.bind(dom),
    IS_REACT_ACT_ENVIRONMENT: true,
})) {
    originals.set(key, Object.getOwnPropertyDescriptor(globalThis, key));
    Object.defineProperty(globalThis, key, { configurable: true, writable: true, value });
}
const { act, useState } = await import("react");
const { createRoot } = await import("react-dom/client");
const { App } = await import("antd");
const { ChannelModelSettings } = await import("../src/pages/settings/channel-video-pricing");
const { apiClient } = await import("../src/services/api/request");
const { createModelChannel, defaultConfig, normalizeConfigSnapshot, resolveModelRequestConfig } = await import("../src/stores/use-config-store");
const { prepareBackendGenerationTask } = await import("../src/services/api/generation-task");
const { defaultModelCapabilityConfig } = await import("../src/lib/model-capabilities");

const originalRequest = apiClient.request;
afterEach(() => {
    apiClient.request = originalRequest;
});
afterAll(() => {
    dom.happyDOM.abort();
    for (const [key, value] of originals) {
        if (value) Object.defineProperty(globalThis, key, value);
        else Reflect.deleteProperty(globalThis, key);
    }
});

function personalChannel(patch: Partial<ModelChannel> = {}) {
    return createModelChannel({
        id: "personal",
        name: "Personal",
        baseUrl: "https://provider.example/v1",
        apiKey: "synthetic-test-key",
        models: ["gpt-image-2-cf"],
        ...patch,
    });
}

async function fixture(initial = personalChannel()) {
    apiClient.request = (async () => ({
        data: {
            code: 0,
            data: {
                providers: [
                    { id: "openai-image", name: "OpenAI Images", categories: ["image"], enabled: true, vendor: "OpenAI", version: "1", create: "POST /v1/images/generations" },
                    { id: "gemini-image", name: "Gemini Images", categories: ["image"], enabled: true, vendor: "Google", version: "1", create: "POST /v1beta/models/{model}:generateContent" },
                    { id: "chat-completion", name: "Chat Completions", categories: ["text"], enabled: true, vendor: "OpenAI", version: "1", create: "POST /v1/chat/completions" },
                ],
            },
        },
    })) as typeof apiClient.request;
    const host = document.createElement("div");
    document.body.append(host);
    let current = initial;
    let writes = 0;
    function Harness() {
        const [channel, setChannel] = useState(initial);
        return (
            <App>
                <ChannelModelSettings
                    channel={channel}
                    onChange={(modelCosts) => {
                        writes++;
                        current = { ...channel, modelCosts };
                        setChannel(current);
                    }}
                />
            </App>
        );
    }
    const root = createRoot(host);
    await act(async () => {
        root.render(<Harness />);
    });
    const button = (text: string) => Array.from(document.querySelectorAll("button")).find((item) => item.textContent?.replace(/\s/g, "") === text)!;
    return {
        host,
        button,
        channel: () => current,
        writes: () => writes,
        radio: (protocol: string) => document.querySelector<HTMLInputElement>(`input[type="radio"][value="${protocol}"]`)!,
        checkbox: (model: string) => document.querySelector<HTMLInputElement>(`input[type="checkbox"][aria-label="选择模型 ${model}"]`)!,
        open: async () => {
            await act(async () => button("配置使用").click());
        },
        dispose: async () => {
            await act(async () => root.unmount());
            host.remove();
        },
    };
}

test("opening an unconfigured image alias neither selects nor saves a guessed protocol", async () => {
    const f = await fixture();
    try {
        expect(f.host.textContent).toContain("待配置请求协议");
        expect(f.host.textContent).not.toContain("POST /v1/images/generations");
        await f.open();
        expect(f.radio("openai-image").checked).toBe(false);
        expect(f.radio("gemini-image").checked).toBe(false);
        expect(f.button("测试模型").disabled).toBe(true);
        expect(f.button("完成").disabled).toBe(true);
        expect(f.writes()).toBe(0);
        const config = { ...defaultConfig, channels: [f.channel()], model: "personal::gpt-image-2-cf" };
        await expect(prepareBackendGenerationTask({ config, mode: "image", prompt: "test" })).rejects.toThrow("未选择可用请求协议");
    } finally {
        await f.dispose();
    }
});

test("one protocol click persists through completion and reload into the real generation payload", async () => {
    const f = await fixture();
    try {
        await f.open();
        await act(async () => f.radio("openai-image").click());
        expect(f.writes()).toBe(1);
        expect(f.radio("openai-image").checked).toBe(true);
        expect(f.channel().modelCosts?.[0]).toMatchObject({ model: "gpt-image-2-cf", capability: "image", protocol: "openai-image" });
        expect(f.button("测试模型").disabled).toBe(false);
        await act(async () => f.button("完成").click());
        await f.open();
        expect(f.radio("openai-image").checked).toBe(true);
        const config = normalizeConfigSnapshot(
            JSON.parse(
                JSON.stringify({
                    config: {
                        ...defaultConfig,
                        channels: [f.channel()],
                        model: "personal::gpt-image-2-cf",
                        imageModel: "personal::gpt-image-2-cf",
                    },
                }),
            ),
        ).config;
        expect(resolveModelRequestConfig(config, config.model).interfaceType).toBe("openai-image");
        const task = await prepareBackendGenerationTask({ config, mode: "image", prompt: "test" });
        expect(task.input.config).toMatchObject({ interfaceType: "openai-image", model: "gpt-image-2-cf", baseUrl: "https://provider.example/v1", channelId: "" });
    } finally {
        await f.dispose();
    }
});

for (const source of ["channel", "model"] as const) {
    test(`saved ${source} protocol stays selected without writing a replacement`, async () => {
        const f = await fixture(
            personalChannel({
                interfaceType: source === "channel" ? "gemini-image" : "openai-image",
                ...(source === "model"
                    ? {
                          modelCosts: [{ model: "gpt-image-2-cf", capability: "image", protocol: "gemini-image", billingMode: "fixed_request", unitPriceMicrocredits: 0 }],
                      }
                    : {}),
            }),
        );
        try {
            await f.open();
            expect(f.radio("gemini-image").checked).toBe(true);
            expect(f.radio("openai-image").checked).toBe(false);
            expect(f.writes()).toBe(0);
            const config = { ...defaultConfig, channels: [f.channel()], model: "personal::gpt-image-2-cf" };
            expect(resolveModelRequestConfig(config, config.model).interfaceType).toBe("gemini-image");
        } finally {
            await f.dispose();
        }
    });
}

test("changing capability requires an explicit protocol instead of saving the first catalog entry", async () => {
    const f = await fixture();
    try {
        await f.open();
        const textCapability = Array.from(document.querySelectorAll("label")).find((item) => item.textContent?.trim() === "文本")!;
        await act(async () => textCapability.click());
        expect(f.channel().modelCosts?.[0]?.capability).toBe("text");
        expect(f.channel().modelCosts?.[0]?.protocol).toBeUndefined();
        expect(f.radio("chat-completion").checked).toBe(false);
        expect(f.button("完成").disabled).toBe(true);
        await act(async () => f.radio("chat-completion").click());
        expect(f.channel().modelCosts?.[0]?.protocol).toBe("chat-completion");
        expect(f.button("完成").disabled).toBe(false);
    } finally {
        await f.dispose();
    }
});

test("applies one protocol to multiple selected models without changing their prices", async () => {
    const f = await fixture(
        personalChannel({
            models: ["gpt-image-2-cf", "gpt-image-1", "text-model"],
            interfaceType: "gemini-image",
            modelCosts: [
                { model: "gpt-image-2-cf", capability: "image", protocol: "gemini-image", billingMode: "per_second", unitPriceMicrocredits: 123 },
                { model: "gpt-image-1", capability: "image", protocol: "gemini-image", billingMode: "fixed_request", unitPriceMicrocredits: 456 },
                { model: "text-model", capability: "text", protocol: "chat-completion", billingMode: "fixed_request", unitPriceMicrocredits: 789 },
            ],
        }),
    );
    try {
        await act(async () => f.checkbox("gpt-image-2-cf").click());
        await act(async () => f.checkbox("gpt-image-1").click());
        expect(f.button("应用到已选模型").disabled).toBe(false);
        await act(async () => f.radio("openai-image").click());
        await act(async () => f.button("应用到已选模型").click());

        expect(f.channel().modelCosts?.find((item) => item.model === "gpt-image-2-cf")).toMatchObject({ protocol: "openai-image", billingMode: "per_second", unitPriceMicrocredits: 123 });
        expect(f.channel().modelCosts?.find((item) => item.model === "gpt-image-1")).toMatchObject({ protocol: "openai-image", billingMode: "fixed_request", unitPriceMicrocredits: 456 });
        expect(f.channel().modelCosts?.find((item) => item.model === "text-model")).toMatchObject({ protocol: "chat-completion", unitPriceMicrocredits: 789 });
    } finally {
        await f.dispose();
    }
});

test("mixed capabilities cannot be assigned one protocol", async () => {
    const f = await fixture(
        personalChannel({
            models: ["gpt-image-2-cf", "text-model"],
            modelCosts: [
                { model: "gpt-image-2-cf", capability: "image", protocol: "openai-image", billingMode: "fixed_request", unitPriceMicrocredits: 0 },
                { model: "text-model", capability: "text", protocol: "chat-completion", billingMode: "fixed_request", unitPriceMicrocredits: 0 },
            ],
        }),
    );
    try {
        await act(async () => f.checkbox("gpt-image-2-cf").click());
        await act(async () => f.checkbox("text-model").click());
        expect(f.button("应用到已选模型").disabled).toBe(true);
        expect(f.writes()).toBe(0);
        await act(async () => f.button("清空选择").click());
        expect(f.checkbox("gpt-image-2-cf").checked).toBe(false);
        expect(f.checkbox("text-model").checked).toBe(false);
    } finally {
        await f.dispose();
    }
});

test("reapplying the same protocol preserves custom capabilities and display name", async () => {
    const capabilityConfig = { version: 1 as const, image: defaultModelCapabilityConfig("openai-image", "gpt-image-2-cf").image };
    capabilityConfig.image!.references.maxImages = 2;
    const f = await fixture(personalChannel({ modelCosts: [{ model: "gpt-image-2-cf", displayName: "Custom image", capability: "image", protocol: "openai-image", capabilityConfig, billingMode: "fixed_request", unitPriceMicrocredits: 123 }] }));
    try {
        await act(async () => f.checkbox("Custom image").click());
        await act(async () => f.button("应用到已选模型").click());
        expect(f.channel().modelCosts?.[0]).toMatchObject({ displayName: "Custom image", capabilityConfig, unitPriceMicrocredits: 123 });
    } finally {
        await f.dispose();
    }
});
