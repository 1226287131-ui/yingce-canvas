package app

import (
	"encoding/json"
	"strings"
)

// Custom models are a user-supplied, immutable run snapshot. Tools only receive
// opaque selectors; connection details never enter model messages or events.
type CloudAgentCustomModel struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Capability string         `json:"capability"`
	Config     map[string]any `json:"config"`
}

const cloudAgentCustomChannelPrefix = "custom:"

func cloudAgentCustomModelByID(req CloudAgentRequest, id string) (*CloudAgentCustomModel, error) {
	for index := range req.CustomModels {
		if req.CustomModels[index].ID == id {
			return &req.CustomModels[index], nil
		}
	}
	return nil, InvalidModelSelection("自定义模型不在本轮已授权目录中，请重新选择模型后发送")
}

func cloudAgentCustomMediaModel(req CloudAgentRequest, a cloudAgentMediaArgs) (*CloudAgentCustomModel, error) {
	item, err := cloudAgentCustomModelByID(req, strings.TrimPrefix(a.ChannelID, cloudAgentCustomChannelPrefix))
	if err != nil {
		return nil, err
	}
	if normalizeCapability(item.Capability) != normalizeCapability(a.Mode) || stringValue(item.Config["model"]) != a.ChannelModelKey {
		return nil, ModelCapabilityNotSupported("自定义模型选择与本轮目录的模型或能力不一致")
	}
	return item, nil
}

func cloudAgentCustomCapability(item CloudAgentCustomModel) (*ModelCapabilityConfig, error) {
	raw, err := json.Marshal(item.Config["capabilityConfig"])
	if err != nil {
		return nil, BadAuthRequest("自定义模型能力配置无效")
	}
	var profile *ModelCapabilityConfig
	if err := json.Unmarshal(raw, &profile); err != nil {
		return nil, BadAuthRequest("自定义模型能力配置无效")
	}
	protocol, name := stringValue(item.Config["interfaceType"]), stringValue(item.Config["model"])
	if profile == nil {
		profile = DefaultModelCapabilityConfigForModel(protocol, name)
	}
	return NormalizeModelCapabilityConfigForModel(item.Capability, protocol, name, profile)
}

func normalizeCloudAgentCustomModels(req *CloudAgentRequest, durable ...bool) error {
	protected := len(durable) > 0 && durable[0]
	if len(req.CustomModels) > 128 {
		return BadAuthRequest("自定义模型目录最多 128 个模型")
	}
	seen := map[string]bool{}
	for index := range req.CustomModels {
		item := &req.CustomModels[index]
		if err := validateCloudAgentID(item.ID, "自定义模型 ID", 240); err != nil || seen[item.ID] {
			return BadAuthRequest("自定义模型 ID 无效或重复")
		}
		seen[item.ID] = true
		item.Capability = normalizeCapability(item.Capability)
		if item.Capability != "text" && !cloudAgentGenerationModeSupported(item.Capability) {
			return BadAuthRequest("自定义模型能力无效")
		}
		// Restrict connection fields to the provider contract. In particular a
		// browser snapshot cannot inject managed routes, prices or credentials.
		config := map[string]any{"channelId": ""}
		for _, key := range []string{
			"baseUrl", "apiKey", "secretKey", "headers", "apiFormat", "interfaceType", "model", "capabilityConfig", "defaultOptions",
			"imageParameters", "size", "imageResolution", "quality", "transparentBackground",
			"videoSeconds", "vquality", "videoGenerateAudio", "videoWatermark", "videoArkPrivateAssetUpload",
			"audioVoice", "audioFormat", "audioSpeed", "audioLanguage", "audioDialect", "audioInstructions",
		} {
			if value, exists := item.Config[key]; exists {
				config[key] = value
			}
		}
		item.Config = config
		raw, err := json.Marshal(config)
		if err != nil || len(raw) > 128<<10 {
			return BadAuthRequest("自定义模型配置无效或过大")
		}
		if !protected && strings.Contains(string(raw), encryptedSettingPrefix) {
			return BadAuthRequest("请提交浏览器中的自定义模型配置")
		}
		if strings.TrimSpace(stringValue(config["baseUrl"])) == "" {
			return BadAuthRequest("自定义模型需要上游地址")
		}
		if protected {
			for _, key := range []string{"baseUrl", "apiKey", "secretKey"} {
				if value := stringValue(config[key]); value != "" && !strings.HasPrefix(value, encryptedSettingPrefix) {
					return BadAuthRequest("自定义模型运行凭据未受保护")
				}
			}
		} else if _, err := ValidateCustomRelayURL(stringValue(config["baseUrl"])); err != nil {
			return BadAuthRequest("自定义模型上游地址不可用或不允许访问")
		}
		var headers []OutboundHeader
		headerJSON, err := json.Marshal(config["headers"])
		if err != nil || json.Unmarshal(headerJSON, &headers) != nil {
			return BadAuthRequest("自定义模型请求头无效")
		}
		if protected {
			for _, header := range headers {
				if header.Value != "" && !strings.HasPrefix(header.Value, encryptedSettingPrefix) {
					return BadAuthRequest("自定义模型运行凭据未受保护")
				}
			}
		} else if _, err := NormalizeOutboundHeaders(headers); err != nil {
			return err
		}
		if err := validateCloudAgentID(stringValue(config["model"]), "自定义模型名称", 160); err != nil {
			return err
		}
		if strings.TrimSpace(item.Name) == "" {
			item.Name = stringValue(config["model"])
		}
		if err := validateCloudAgentID(item.Name, "自定义模型显示名", 240); err != nil {
			return err
		}
		profile, err := cloudAgentCustomCapability(*item)
		if err != nil {
			return err
		}
		config["capabilityConfig"] = profile
	}
	if req.CustomModelID != "" {
		item, err := cloudAgentCustomModelByID(*req, req.CustomModelID)
		if err != nil {
			return err
		}
		if item.Capability != "text" {
			return ModelCapabilityNotSupported("Agent 主模型必须支持文本")
		}
		if req.Model != "" && req.Model != stringValue(item.Config["model"]) {
			return BadAuthRequest("自定义模型标识与 model 不一致")
		}
		req.Model = stringValue(item.Config["model"])
	}
	return nil
}

func (s *Service) protectCloudAgentCustomModels(req *CloudAgentRequest) error {
	for index := range req.CustomModels {
		// Normalize to JSON maps before recursive protection: typed capability
		// structs and header slices must not evade the task secret walker.
		normalized, err := normalizeTaskInput(map[string]any{"config": req.CustomModels[index].Config})
		if err != nil {
			return err
		}
		if err := s.protectTaskSecrets(normalized); err != nil {
			return err
		}
		req.CustomModels[index].Config = normalized["config"].(map[string]any)
	}
	return nil
}

func cloudAgentModelConfig(req CloudAgentRequest) map[string]any {
	if req.CustomModelID != "" {
		if item, err := cloudAgentCustomModelByID(req, req.CustomModelID); err == nil {
			return cloudAgentCopyConfig(item.Config)
		}
	}
	return map[string]any{"channelId": req.ChannelID, "channelModelKey": req.ChannelModelKey, "model": firstNonEmpty(req.ChannelModelKey, req.Model)}
}

func cloudAgentCopyConfig(source map[string]any) map[string]any {
	copy := make(map[string]any, len(source))
	for key, value := range source {
		copy[key] = value
	}
	return copy
}
