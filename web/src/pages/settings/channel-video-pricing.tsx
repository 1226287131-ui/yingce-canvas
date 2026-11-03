import { useEffect, useState } from "react";
import { App, Button, Checkbox, Segmented, Tag } from "antd";
import { ChevronRight, FlaskConical, Settings2 } from "lucide-react";

import { ModelEditorModal } from "@/components/model-editor-modal";
import { ModelProtocolBrowser } from "@/components/model-protocol-browser";
import { testChannelModelConnection } from "@/lib/model-connection-test";
import { ModelCapabilityEditor } from "@/components/model-capability-editor";
import { type ModelCapabilityChoice } from "@/components/model-protocol-picker";
import { defaultModelCapabilityConfig } from "@/lib/model-capabilities";
import { modelProtocolCapability, modelProtocolDefinition, type ModelProtocol, type ModelProtocolDefinition } from "@/lib/model-protocols";
import { fetchPluginProviderCatalog } from "@/services/api/plugin-catalog";
import { modelOptionName, resolveChannelModelProtocol, type ModelChannel } from "@/stores/use-config-store";

type ModelCost = NonNullable<ModelChannel["modelCosts"]>[number];

export function ChannelModelSettings({ channel, onChange }: { channel: ModelChannel; onChange: (costs: ModelCost[]) => void }) {
    const { message } = App.useApp();
    const [testingModel, setTestingModel] = useState("");
    const [editorTab, setEditorTab] = useState("protocol");
    const [protocolLoading, setProtocolLoading] = useState(true);
    const [protocolError, setProtocolError] = useState("");
    const [activeModel, setActiveModel] = useState<string | null>(null);
    const [selectedModels, setSelectedModels] = useState<string[]>([]);
    const [batchProtocol, setBatchProtocol] = useState("");
    const [availableProtocols, setAvailableProtocols] = useState<ModelProtocolDefinition[]>([]);

    useEffect(() => {
        let active = true;
        void fetchPluginProviderCatalog("user.custom-channel")
            .then((items) => {
                if (active) setAvailableProtocols(items);
            })
            .catch((error) => {
                if (active) setProtocolError(error instanceof Error ? error.message : "协议目录读取失败");
            })
            .finally(() => {
                if (active) setProtocolLoading(false);
            });
        return () => {
            active = false;
        };
    }, []);

    const modelNames = channel.models.map(modelOptionName);
    const selectedModelNames = selectedModels.filter((model) => modelNames.includes(model));

    useEffect(() => {
        setSelectedModels((current) => current.filter((model) => modelNames.includes(model)));
    }, [modelNames.join("\u0000")]);

    useEffect(() => {
        if (!selectedModelNames.length) {
            setBatchProtocol("");
            return;
        }
        const protocols = selectedModelNames.map((model) => resolveChannelModelProtocol(channel, model));
        setBatchProtocol(protocols.every((protocol) => protocol === protocols[0]) ? protocols[0] || "" : "");
    }, [selectedModelNames.join("\u0000"), channel.modelCosts, channel.interfaceType]);

    if (!channel.models.length) return null;

    const updateCost = (model: string, patch: Partial<ModelCost>) => {
        const defaultProtocol = resolveChannelModelProtocol(channel, model);
        const defaultCap = modelProtocolCapability(defaultProtocol, availableProtocols) || inferCapabilityFromModel(model);
        const current = channel.modelCosts?.find((item) => item.model === model) || {
            model,
            capability: defaultCap,
            protocol: defaultProtocol,
            billingMode: "fixed_request" as const,
            unitPriceMicrocredits: 0,
            capabilityConfig: defaultProtocol ? defaultModelCapabilityConfig(defaultProtocol, model) : undefined,
        };
        const next = [...(channel.modelCosts || []).filter((item) => item.model !== model), { ...current, ...patch, model }];
        onChange(next.filter((item) => channel.models.includes(item.model)));
    };

    const selectedCapabilities = Array.from(
        new Set(
            selectedModelNames.map((model) => {
                const cost = channel.modelCosts?.find((item) => item.model === model);
                const protocol = resolveChannelModelProtocol(channel, model);
                return cost?.capability || modelProtocolCapability(protocol, availableProtocols) || inferCapabilityFromModel(model);
            }),
        ),
    );
    const batchCapability = selectedCapabilities.length === 1 ? selectedCapabilities[0] : undefined;
    const batchProtocolDefinition = modelProtocolDefinition(batchProtocol, availableProtocols);
    const canApplyBatchProtocol = Boolean(
        selectedModelNames.length > 0 && batchCapability && batchProtocol && batchProtocolDefinition && batchProtocolDefinition.enabled !== false && batchProtocolDefinition.capability === batchCapability && !protocolLoading && !protocolError,
    );

    const applyBatchProtocol = () => {
        if (!canApplyBatchProtocol || !batchCapability) return;
        const nextCosts = [...(channel.modelCosts || []).filter((item) => modelNames.includes(item.model))];
        for (const model of selectedModelNames) {
            const current = nextCosts.find((item) => item.model === model) || {
                model,
                capability: batchCapability,
                protocol: resolveChannelModelProtocol(channel, model),
                billingMode: "fixed_request" as const,
                unitPriceMicrocredits: 0,
                capabilityConfig: undefined,
            };
            const updated = {
                ...current,
                model,
                capability: batchCapability,
                protocol: batchProtocol,
                billingMode: current.billingMode,
                unitPriceMicrocredits: current.unitPriceMicrocredits ?? 0,
                capabilityConfig:
                    batchCapability === "text" || batchCapability === "image" || batchCapability === "video"
                        ? current.protocol === batchProtocol
                            ? current.capabilityConfig || defaultModelCapabilityConfig(batchProtocol, model)
                            : defaultModelCapabilityConfig(batchProtocol, model)
                        : undefined,
            };
            const index = nextCosts.findIndex((item) => item.model === model);
            if (index >= 0) nextCosts[index] = updated;
            else nextCosts.push(updated);
        }
        onChange(nextCosts.filter((item) => modelNames.includes(item.model)));
    };

    const testModel = async (model: string, capability: ModelCost["capability"], protocol: ModelProtocol) => {
        setTestingModel(model);
        try {
            const detail = await testChannelModelConnection(channel, model, capability, protocol);
            message.success(`模型测试通过：${detail}`);
        } catch (error) {
            message.error(error instanceof Error ? error.message : "模型测试失败");
        } finally {
            setTestingModel("");
        }
    };

    const activeModelCost = activeModel ? channel.modelCosts?.find((item) => item.model === activeModel) : undefined;
    const activeProtocol = (activeModel ? resolveChannelModelProtocol(channel, activeModel) : undefined) || "";
    const activeCapability = activeModelCost?.capability || modelProtocolCapability(activeProtocol, availableProtocols) || (activeModel ? inferCapabilityFromModel(activeModel) : "text");
    const activeProtocolDefinition = modelProtocolDefinition(activeProtocol, availableProtocols);
    const activeProtocolAvailable = Boolean(activeProtocolDefinition && activeProtocolDefinition.enabled !== false && activeProtocolDefinition.capability === activeCapability);

    return (
        <div className="mt-4">
            <div className="mb-2 flex items-center justify-between gap-3">
                <div>
                    <div className="text-xs font-medium">模型能力与请求协议</div>
                    <div className="mt-0.5 text-[var(--fs-tiny)] text-foreground/42">与运营后台使用同一能力目录；测试会发起真实请求并可能产生供应商费用</div>
                </div>
                <span className="text-[var(--fs-tiny)] text-foreground/35">{channel.models.length} 个模型</span>
            </div>
            <div className="mb-2 flex flex-wrap items-center justify-between gap-2 rounded-md border border-border/70 bg-background px-3 py-2">
                <Checkbox
                    checked={selectedModelNames.length === modelNames.length && modelNames.length > 0}
                    indeterminate={selectedModelNames.length > 0 && selectedModelNames.length < modelNames.length}
                    onChange={(event) => setSelectedModels(event.target.checked ? modelNames : [])}
                >
                    全选模型
                </Checkbox>
                <span className="text-[var(--fs-tiny)] text-foreground/48">{selectedModelNames.length ? `已选 ${selectedModelNames.length} 个模型` : "可多选后统一设置请求协议"}</span>
            </div>
            {selectedModelNames.length ? (
                <div className="mb-3 space-y-3 rounded-md border border-border/70 bg-surface-active p-3">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                        <div>
                            <div className="text-xs font-medium">批量设置请求协议</div>
                            <div className="mt-0.5 text-[var(--fs-tiny)] text-foreground/48">{batchCapability ? `已选模型均为${capabilityLabel(batchCapability)}能力` : "请选择相同能力的模型后再批量配置"}</div>
                        </div>
                        <Button size="small" type="text" onClick={() => setSelectedModels([])}>
                            清空选择
                        </Button>
                    </div>
                    {batchCapability ? (
                        <ModelProtocolBrowser loading={protocolLoading} error={protocolError} capability={batchCapability} value={batchProtocol} protocols={availableProtocols} onChange={setBatchProtocol} />
                    ) : (
                        <p className="text-xs text-foreground/50">批量设置要求所选模型使用同一种能力，例如都选择图片或都选择视频。</p>
                    )}
                    <div className="flex justify-end">
                        <Button type="primary" disabled={!canApplyBatchProtocol} onClick={applyBatchProtocol}>
                            应用到已选模型
                        </Button>
                    </div>
                </div>
            ) : null}
            <div className="space-y-2">
                {channel.models.map((rawModel) => {
                    const model = modelOptionName(rawModel);
                    const cost = channel.modelCosts?.find((item) => item.model === model);
                    const protocol = resolveChannelModelProtocol(channel, model);
                    const capability = cost?.capability || modelProtocolCapability(protocol, availableProtocols) || inferCapabilityFromModel(model);
                    const displayName = cost?.displayName?.trim() || model;
                    return (
                        <div key={model} className="flex min-w-0 items-center gap-3 rounded-md bg-surface-active px-3 py-2.5 transition-colors hover:bg-surface-hover">
                            <Checkbox
                                aria-label={`选择模型 ${displayName}`}
                                checked={selectedModelNames.includes(model)}
                                onChange={(event) => setSelectedModels((current) => (event.target.checked ? Array.from(new Set([...current, model])) : current.filter((item) => item !== model)))}
                            />
                            <span className="grid size-8 shrink-0 place-items-center rounded-md bg-foreground/[.045] text-foreground/65">
                                <Settings2 className="size-4" />
                            </span>
                            <div className="min-w-0 flex-1">
                                <div className="truncate text-xs font-medium" title={displayName === model ? model : `${displayName} (${model})`}>
                                    {displayName}
                                </div>
                                <div className="mt-1 flex min-w-0 flex-wrap items-center gap-1.5">
                                    <Tag className="mr-0 text-[var(--fs-tiny)]" bordered={false}>
                                        {capabilityLabel(capability)}
                                    </Tag>
                                    <span className="truncate font-mono text-[var(--fs-tiny)] text-foreground/40" title={modelProtocolDefinition(protocol, availableProtocols)?.create}>
                                        {modelProtocolDefinition(protocol, availableProtocols)?.create || protocol || "待配置请求协议"}
                                    </span>
                                </div>
                            </div>
                            <Button
                                type="text"
                                size="small"
                                icon={<ChevronRight className="size-4" />}
                                iconPosition="end"
                                onClick={() => {
                                    setEditorTab("protocol");
                                    setActiveModel(model);
                                }}
                            >
                                配置使用
                            </Button>
                        </div>
                    );
                })}
            </div>
            <ModelEditorModal
                open={Boolean(activeModel)}
                busy={Boolean(testingModel)}
                title="编辑模型使用配置"
                subtitle={activeModel || ""}
                activeKey={editorTab}
                onTabChange={setEditorTab}
                onClose={() => setActiveModel(null)}
                footer={
                    <div className="model-editor-footer">
                        <span className="text-xs text-foreground/50">{activeProtocolAvailable ? "更改实时保存到个人渠道配置" : "请明确选择可用的请求协议后再生成"}</span>
                        <div className="model-editor-footer-actions">
                            <Button
                                icon={<FlaskConical className="size-4" />}
                                loading={Boolean(testingModel)}
                                disabled={!activeProtocolAvailable || protocolLoading || Boolean(protocolError)}
                                onClick={() => {
                                    if (activeModel && activeProtocol) void testModel(activeModel, activeCapability, activeProtocol);
                                }}
                            >
                                测试模型
                            </Button>
                            <Button type="primary" disabled={Boolean(testingModel) || !activeProtocolAvailable || protocolLoading || Boolean(protocolError)} onClick={() => setActiveModel(null)}>
                                完成
                            </Button>
                        </div>
                    </div>
                }
                items={
                    activeModel
                        ? [
                              {
                                  key: "protocol",
                                  label: "基本信息",
                                  children: (
                                      <div className="space-y-4" inert={Boolean(testingModel)}>
                                          <section className="space-y-2">
                                              <div className="text-xs font-medium">模型能力</div>
                                              <Segmented<ModelCapabilityChoice>
                                                  block
                                                  options={[
                                                      { label: "文本", value: "text" },
                                                      { label: "图片", value: "image" },
                                                      { label: "视频", value: "video" },
                                                      { label: "音频", value: "audio" },
                                                  ]}
                                                  value={activeCapability}
                                                  onChange={(nextCapability) => {
                                                      const nextProtocol = availableProtocols.find((item) => item.value === activeProtocol && item.capability === nextCapability && item.enabled !== false)?.value;
                                                      updateCost(activeModel, {
                                                          protocol: nextProtocol,
                                                          capability: nextCapability,
                                                          billingMode: "fixed_request",
                                                          unitPriceMicrocredits: 0,
                                                          capabilityConfig: nextProtocol && nextCapability !== "audio" ? defaultModelCapabilityConfig(nextProtocol, activeModel) : undefined,
                                                      });
                                                  }}
                                              />
                                          </section>
                                          <section className="space-y-2">
                                              <div className="text-xs font-medium">请求协议</div>
                                              <ModelProtocolBrowser
                                                  loading={protocolLoading}
                                                  error={protocolError}
                                                  capability={activeCapability}
                                                  value={activeProtocol}
                                                  protocols={availableProtocols}
                                                  onChange={(nextProtocol) =>
                                                      updateCost(activeModel, {
                                                          protocol: nextProtocol,
                                                          capability: activeCapability,
                                                          billingMode: "fixed_request",
                                                          unitPriceMicrocredits: 0,
                                                          capabilityConfig: activeCapability === "text" || activeCapability === "image" || activeCapability === "video" ? defaultModelCapabilityConfig(nextProtocol, activeModel) : undefined,
                                                      })
                                                  }
                                              />
                                          </section>
                                      </div>
                                  ),
                              },
                              {
                                  key: "capabilities",
                                  label: "能力与参数",
                                  children: (
                                      <div inert={Boolean(testingModel)}>
                                          {activeCapability === "text" || activeCapability === "image" || activeCapability === "video" ? (
                                              <ModelCapabilityEditor
                                                  capability={activeCapability}
                                                  model={activeModel}
                                                  value={activeModelCost?.capabilityConfig || defaultModelCapabilityConfig(activeProtocol, activeModel)}
                                                  protocol={activeProtocol}
                                                  workflows={modelProtocolDefinition(activeProtocol, availableProtocols)?.workflows}
                                                  imageParameters={modelProtocolDefinition(activeProtocol, availableProtocols)?.imageParameters}
                                                  onChange={(capabilityConfig) => updateCost(activeModel, { capabilityConfig })}
                                              />
                                          ) : (
                                              <p className="text-xs text-foreground/50">当前模型类型无需额外配置引用与参数。</p>
                                          )}
                                      </div>
                                  ),
                              },
                          ]
                        : []
                }
            />
        </div>
    );
}

function inferCapabilityFromModel(model: string): ModelCapabilityChoice {
    const lower = model.toLowerCase();
    if (
        lower.includes("seedream") ||
        lower.includes("image") ||
        lower.includes("dall-e") ||
        lower.includes("dalle") ||
        lower.includes("flux") ||
        lower.includes("imagen") ||
        lower.includes("banana") ||
        lower.includes("midjourney") ||
        lower.includes("sdxl") ||
        lower.includes("stable-diffusion")
    ) {
        return "image";
    }
    if (
        lower.includes("video") ||
        lower.includes("sora") ||
        lower.includes("veo") ||
        lower.includes("kling") ||
        lower.includes("seedance") ||
        lower.includes("minimax") ||
        lower.includes("hailuo") ||
        lower.includes("pika") ||
        lower.includes("runway") ||
        lower.includes("omni") ||
        lower.includes("cogvideo") ||
        lower.includes("wan")
    ) {
        return "video";
    }
    if (lower.includes("audio") || lower.includes("tts") || lower.includes("voice") || lower.includes("speech") || lower.includes("sound") || lower.includes("music")) {
        return "audio";
    }
    return "text";
}

function capabilityLabel(value: ModelCost["capability"]) {
    return { text: "文本", image: "图片", video: "视频", audio: "音频", "": "待配置" }[value] || "待配置";
}
