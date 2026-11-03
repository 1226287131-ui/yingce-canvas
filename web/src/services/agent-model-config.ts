import type { AgentCustomModel, CreateAgentRunInput } from "@/services/api/agent";
import type { AgentMemoryCompactRequest } from "@/services/api/agent-memories";
import { backendProviderConfig } from "@/services/api/generation-task";
import { encodeChannelModel, logicalModelIDForConfig, modelDisplayName, modelOptionName, resolveModelChannel, resolveModelRequestConfig, selectableModelsByCapability, type AiConfig } from "@/stores/use-config-store";
import { agentRequestFingerprint } from "@/lib/agent-request-identity";

export { agentRequestFingerprint } from "@/lib/agent-request-identity";

const defaultTextProtocols = { openai: "chat-completion", claude: "claude-api", gemini: "gemini-generate-content" } as const;

export function agentCustomModelId(config: AiConfig, model: string) {
    const channel = resolveModelChannel(config, model);
    return encodeChannelModel(channel.id, modelOptionName(model));
}

export function agentModelSelection(config: AiConfig, model: string): Pick<CreateAgentRunInput, "logicalModelId" | "channelId" | "channelModelKey" | "customModelId"> {
    const logicalModelId = logicalModelIDForConfig({ ...config, model });
    if (logicalModelId) return { logicalModelId };
    const channel = resolveModelChannel(config, model);
    if (channel.scope === "system") return { channelId: channel.id, channelModelKey: modelOptionName(model) };
    const customModelId = agentCustomModelId(config, model);
    if (!selectableModelsByCapability(config).includes(customModelId)) throw new Error("所选自定义模型已移除或停用，请重新选择模型");
    if (!agentCustomModels(config).some((entry) => entry.id === customModelId)) throw new Error("所选自定义模型缺少上游地址或请求协议，请先完善渠道配置");
    return { customModelId };
}

// Credentials travel only in the authenticated admission request. The backend
// encrypts this snapshot so queued steps and media tools can continue offline.
export function agentCustomModels(config: AiConfig): AgentCustomModel[] {
    return (["text", "image", "video", "audio"] as const).flatMap((capability) =>
        selectableModelsByCapability(config, capability).flatMap((model) => {
            const channel = resolveModelChannel(config, model);
            if (channel.scope === "system") return [];
            const selected = { ...config, model, taskWorkflowProvider: "model" as const };
            const request = resolveModelRequestConfig(selected, model);
            // Manually added text models use the channel connection format until
            // a model or channel protocol is explicitly selected.
            const interfaceType = request.interfaceType || (capability === "text" ? defaultTextProtocols[request.apiFormat] : undefined);
            if (!request.baseUrl.trim() || !interfaceType) return [];
            const defaults = channel.modelCosts?.find((entry) => entry.model === modelOptionName(model))?.defaultOptions;
            return [{ id: agentCustomModelId(config, model), name: `${modelDisplayName(config, model)}（${channel.name}）`, capability, config: { ...backendProviderConfig(selected, capability), interfaceType, ...(defaults ? { defaultOptions: defaults } : {}) } }];
        }),
    );
}

export function agentMemoryModelSelection(config: AiConfig, selectedModel: string): AgentMemoryCompactRequest {
    const model = selectedModel.trim();
    if (!model) return {};
    const selection = agentModelSelection(config, model);
    const customModels = selection.customModelId ? agentCustomModels(config).filter((entry) => entry.capability === "text" && entry.id === selection.customModelId) : [];
    return {
        ...selection,
        ...(customModels.length ? { customModels } : {}),
        model: modelOptionName(model),
    };
}

export async function restoreAgentRequestModels(request: CreateAgentRunInput, fingerprint: string | undefined, config: AiConfig): Promise<CreateAgentRunInput> {
    if (!fingerprint || request.customModels) return request;
    const customModels = agentCustomModels(config);
    if (await agentRequestFingerprint(customModels) !== fingerprint) throw new Error("上次请求使用的自定义 API 配置已更改，请恢复原配置后重试，避免重复创建任务");
    return { ...request, customModels };
}
