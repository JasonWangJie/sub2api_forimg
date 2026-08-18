package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

const ImageWorkbenchCapabilitySchemaVersion = "image-workbench-v1"

const (
	ImageWorkbenchModeRealtime = "realtime"
	ImageWorkbenchModeAsync    = "async"
)

const (
	ImageWorkbenchProtocolOpenAIImages = "openai_images"
	ImageWorkbenchProtocolOpenAIAsync  = "openai_async"
	ImageWorkbenchProtocolGeminiNative = "gemini_native"
	ImageWorkbenchProtocolGeminiSC     = "gemini_sc"
	ImageWorkbenchProtocolGrokImages   = "grok_images"
)

type imageWorkbenchAPIKeyReader interface {
	GetByID(ctx context.Context, id int64) (*APIKey, error)
	AttachImagePlatformGroups(ctx context.Context, apiKey *APIKey) error
	GetGroupByID(ctx context.Context, groupID int64) (*Group, error)
}

type imageWorkbenchModelCatalog interface {
	GetAvailableModels(ctx context.Context, groupID *int64, platform string) []string
}

type ImageWorkbenchService struct {
	apiKeys imageWorkbenchAPIKeyReader
	models  imageWorkbenchModelCatalog
}

type ImageWorkbenchModel struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type ImageWorkbenchEndpoints struct {
	Generation string `json:"generation,omitempty"`
	Edit       string `json:"edit,omitempty"`
	Upload     string `json:"upload,omitempty"`
	Query      string `json:"query,omitempty"`
}

type ImageWorkbenchCapabilities struct {
	CapabilityVersion       string                  `json:"capability_version"`
	APIKeyID                int64                   `json:"api_key_id"`
	GroupID                 *int64                  `json:"group_id,omitempty"`
	Platform                string                  `json:"platform,omitempty"`
	Available               bool                    `json:"available"`
	UnavailableReason       string                  `json:"unavailable_reason,omitempty"`
	ExecutionMode           string                  `json:"execution_mode,omitempty"`
	Protocol                string                  `json:"protocol,omitempty"`
	Models                  []ImageWorkbenchModel   `json:"models"`
	Endpoints               ImageWorkbenchEndpoints `json:"endpoints"`
	SupportsReferenceImages bool                    `json:"supports_reference_images"`
	MaxOutputImages         int                     `json:"max_output_images"`
	MaxReferenceImages      int                     `json:"max_reference_images"`
	ImageSizes              []string                `json:"image_sizes"`
	AspectRatios            []string                `json:"aspect_ratios"`
	Qualities               []string                `json:"qualities"`
	Formats                 []string                `json:"formats"`
	Backgrounds             []string                `json:"backgrounds"`
}

func NewImageWorkbenchService(apiKeys imageWorkbenchAPIKeyReader, models imageWorkbenchModelCatalog) *ImageWorkbenchService {
	return &ImageWorkbenchService{apiKeys: apiKeys, models: models}
}

func (s *ImageWorkbenchService) GetCapabilities(ctx context.Context, userID, apiKeyID int64) (*ImageWorkbenchCapabilities, error) {
	if s == nil || s.apiKeys == nil || userID <= 0 || apiKeyID <= 0 {
		return nil, infraerrors.NotFound("IMAGE_WORKBENCH_KEY_NOT_FOUND", "API key not found")
	}
	apiKey, err := s.apiKeys.GetByID(ctx, apiKeyID)
	if err != nil || apiKey == nil || apiKey.UserID != userID {
		return nil, infraerrors.NotFound("IMAGE_WORKBENCH_KEY_NOT_FOUND", "API key not found")
	}
	_ = s.apiKeys.AttachImagePlatformGroups(ctx, apiKey)

	capabilities := &ImageWorkbenchCapabilities{
		APIKeyID:     apiKey.ID,
		GroupID:      apiKey.GroupID,
		Available:    false,
		Models:       []ImageWorkbenchModel{},
		ImageSizes:   []string{},
		AspectRatios: []string{},
		Qualities:    []string{},
		Formats:      []string{},
		Backgrounds:  []string{},
	}
	if apiKey.Status != StatusActive {
		capabilities.UnavailableReason = "api_key_inactive"
		return finalizeImageWorkbenchCapabilities(capabilities), nil
	}

	group, reason := s.resolveImageWorkbenchBillingGroup(ctx, apiKey)
	if group == nil {
		capabilities.UnavailableReason = reason
		return finalizeImageWorkbenchCapabilities(capabilities), nil
	}
	groupID := group.ID
	capabilities.GroupID = &groupID
	capabilities.Platform = strings.ToLower(strings.TrimSpace(group.Platform))
	configureImageWorkbenchPlatform(capabilities, group)
	mergeImageWorkbenchAsyncEndpoints(capabilities, s.collectImageWorkbenchAsyncGroups(ctx, apiKey))
	if capabilities.Protocol == "" {
		capabilities.UnavailableReason = "platform_not_supported"
		return finalizeImageWorkbenchCapabilities(capabilities), nil
	}

	availableModels := []string(nil)
	if s.models != nil {
		availableModels = s.models.GetAvailableModels(ctx, &groupID, capabilities.Platform)
	}
	capabilities.Models = imageWorkbenchModelsForGroup(group, availableModels)
	for _, extra := range s.collectImageWorkbenchAsyncGroups(ctx, apiKey) {
		if extra == nil || extra.ID == group.ID {
			continue
		}
		extraModels := []string(nil)
		if s.models != nil {
			gid := extra.ID
			extraModels = s.models.GetAvailableModels(ctx, &gid, extra.Platform)
		}
		capabilities.Models = mergeImageWorkbenchModels(capabilities.Models, imageWorkbenchModelsForGroup(extra, extraModels))
	}
	if len(capabilities.Models) == 0 {
		capabilities.UnavailableReason = "no_image_models"
		return finalizeImageWorkbenchCapabilities(capabilities), nil
	}
	capabilities.Available = true
	return finalizeImageWorkbenchCapabilities(capabilities), nil
}

func (s *ImageWorkbenchService) resolveImageWorkbenchBillingGroup(ctx context.Context, apiKey *APIKey) (*Group, string) {
	if apiKey == nil {
		return nil, "group_required"
	}
	if apiKey.Group != nil && apiKey.GroupID != nil && *apiKey.GroupID == apiKey.Group.ID {
		if apiKey.Group.Status != StatusActive {
			return nil, "group_inactive"
		}
		if GroupAllowsImageGeneration(apiKey.Group) {
			return apiKey.Group, ""
		}
	}
	for _, platform := range []string{PlatformGemini, PlatformOpenAI} {
		groupID, ok := ResolveAsyncImageBillingGroupID(apiKey, platform)
		if !ok {
			continue
		}
		if apiKey.Group != nil && apiKey.Group.ID == groupID {
			if apiKey.Group.Status == StatusActive && GroupAllowsImageGeneration(apiKey.Group) {
				return apiKey.Group, ""
			}
			continue
		}
		group, err := s.apiKeys.GetGroupByID(ctx, groupID)
		if err != nil || group == nil || group.Status != StatusActive || !GroupAllowsImageGeneration(group) {
			continue
		}
		return group, ""
	}
	if apiKey.Group == nil || apiKey.GroupID == nil {
		return nil, "group_required"
	}
	if apiKey.Group.Status != StatusActive {
		return nil, "group_inactive"
	}
	if !GroupAllowsImageGeneration(apiKey.Group) {
		return nil, "image_generation_disabled"
	}
	return apiKey.Group, ""
}

func (s *ImageWorkbenchService) collectImageWorkbenchAsyncGroups(ctx context.Context, apiKey *APIKey) []*Group {
	out := make([]*Group, 0, 2)
	seen := map[int64]struct{}{}
	for _, platform := range []string{PlatformGemini, PlatformOpenAI} {
		groupID, ok := ResolveAsyncImageBillingGroupID(apiKey, platform)
		if !ok {
			continue
		}
		if _, exists := seen[groupID]; exists {
			continue
		}
		var group *Group
		if apiKey.Group != nil && apiKey.Group.ID == groupID {
			group = apiKey.Group
		} else {
			loaded, err := s.apiKeys.GetGroupByID(ctx, groupID)
			if err != nil || loaded == nil {
				continue
			}
			group = loaded
		}
		if group.Status != StatusActive || !GroupAllowsImageGeneration(group) || !group.AllowAsyncImageGeneration {
			continue
		}
		seen[groupID] = struct{}{}
		out = append(out, group)
	}
	return out
}

func mergeImageWorkbenchAsyncEndpoints(out *ImageWorkbenchCapabilities, groups []*Group) {
	if out == nil || len(groups) == 0 {
		return
	}
	hasGemini, hasOpenAI := false, false
	for _, group := range groups {
		switch strings.ToLower(strings.TrimSpace(group.Platform)) {
		case PlatformGemini:
			hasGemini = true
		case PlatformOpenAI:
			hasOpenAI = true
		}
	}
	if !hasGemini || !hasOpenAI {
		return
	}
	// Dual-use keys expose both async surfaces while keeping the preferred platform/protocol.
	out.ExecutionMode = ImageWorkbenchModeAsync
	out.Endpoints.Query = "/v1/images/tasks_async/{task_id}"
	if out.Platform == PlatformOpenAI {
		out.Protocol = ImageWorkbenchProtocolOpenAIAsync
		out.Endpoints.Generation = "/v1/images/generations_oa"
		out.Endpoints.Edit = "/v1/images/generations_oa"
		out.Endpoints.Upload = "/v1/uploads/images_sc"
	} else {
		out.Protocol = ImageWorkbenchProtocolGeminiSC
		out.Endpoints.Generation = "/v1/images/generations_sc"
		out.Endpoints.Upload = "/v1/uploads/images_sc"
		out.Endpoints.Edit = "/v1/images/generations_oa"
	}
}

func mergeImageWorkbenchModels(base, extra []ImageWorkbenchModel) []ImageWorkbenchModel {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]ImageWorkbenchModel, 0, len(base)+len(extra))
	for _, model := range base {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, model)
	}
	for _, model := range extra {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, model)
	}
	return out
}

func configureImageWorkbenchPlatform(out *ImageWorkbenchCapabilities, group *Group) {
	if out == nil || group == nil {
		return
	}
	switch out.Platform {
	case PlatformOpenAI:
		out.SupportsReferenceImages = true
		out.MaxOutputImages = 4
		out.MaxReferenceImages = 5
		out.ImageSizes = openAIImageWorkbenchResolutions()
		out.AspectRatios = openAIImageWorkbenchAspectRatios()
		out.Qualities = []string{"auto", "low", "medium", "high"}
		out.Formats = []string{"png", "jpeg", "webp"}
		out.Backgrounds = []string{"auto", "opaque", "transparent"}
		if group.AllowAsyncImageGeneration {
			out.ExecutionMode = ImageWorkbenchModeAsync
			out.Protocol = ImageWorkbenchProtocolOpenAIAsync
			out.Endpoints = ImageWorkbenchEndpoints{
				Generation: "/v1/images/generations_oa",
				Edit:       "/v1/images/generations_oa",
				Query:      "/v1/images/tasks_async/{task_id}",
			}
		} else {
			out.ExecutionMode = ImageWorkbenchModeRealtime
			out.Protocol = ImageWorkbenchProtocolOpenAIImages
			out.Endpoints = ImageWorkbenchEndpoints{Generation: "/v1/images/generations", Edit: "/v1/images/edits"}
		}
	case PlatformGemini:
		out.SupportsReferenceImages = true
		out.MaxOutputImages = 1
		out.MaxReferenceImages = 5
		out.ImageSizes = []string{"1K", "2K", "4K"}
		out.AspectRatios = []string{"auto", "1:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9"}
		if group.AllowAsyncImageGeneration {
			out.ExecutionMode = ImageWorkbenchModeAsync
			out.Protocol = ImageWorkbenchProtocolGeminiSC
			out.Endpoints = ImageWorkbenchEndpoints{
				Generation: "/v1/images/generations_sc",
				Upload:     "/v1/uploads/images_sc",
				Query:      "/v1/images/tasks_async/{task_id}",
			}
		} else {
			out.ExecutionMode = ImageWorkbenchModeRealtime
			out.Protocol = ImageWorkbenchProtocolGeminiNative
			out.Endpoints = ImageWorkbenchEndpoints{Generation: "/v1beta/models/{model}:generateContent"}
		}
	case PlatformGrok:
		out.SupportsReferenceImages = true
		out.MaxOutputImages = 1
		out.MaxReferenceImages = 1
		out.ExecutionMode = ImageWorkbenchModeRealtime
		out.Protocol = ImageWorkbenchProtocolGrokImages
		out.Endpoints = ImageWorkbenchEndpoints{Generation: "/v1/images/generations", Edit: "/v1/images/edits"}
	}
}

func imageWorkbenchModelsForGroup(group *Group, available []string) []ImageWorkbenchModel {
	if group == nil {
		return []ImageWorkbenchModel{}
	}
	fallback, labels := defaultImageWorkbenchModels(group.Platform)
	source := expandImageWorkbenchModelPatterns(available, fallback)
	if len(source) == 0 {
		source = fallback
	}

	if group.ModelsListConfig.Enabled {
		source = mergeImageWorkbenchModelIDs(source, fallback)
		selected := make([]string, 0, len(group.ModelsListConfig.Models))
		for _, model := range group.ModelsListConfig.Models {
			model = strings.TrimSpace(model)
			if model == "" {
				continue
			}
			if strings.HasSuffix(model, "*") {
				prefix := strings.TrimSuffix(model, "*")
				for _, candidate := range source {
					if strings.HasPrefix(candidate, prefix) {
						selected = append(selected, candidate)
					}
				}
				continue
			}
			if isImageWorkbenchModel(group.Platform, model) {
				selected = append(selected, model)
			}
		}
		source = compactUniqueStrings(selected)
	}

	out := make([]ImageWorkbenchModel, 0, len(source))
	for _, model := range source {
		if !isImageWorkbenchModel(group.Platform, model) {
			continue
		}
		label := labels[model]
		if label == "" {
			label = model
		}
		out = append(out, ImageWorkbenchModel{ID: model, Label: label})
	}
	return out
}

func expandImageWorkbenchModelPatterns(available, fallback []string) []string {
	expanded := make([]string, 0, len(available))
	for _, model := range available {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if !strings.HasSuffix(model, "*") {
			expanded = append(expanded, model)
			continue
		}
		prefix := strings.TrimSuffix(model, "*")
		for _, candidate := range fallback {
			if strings.HasPrefix(candidate, prefix) {
				expanded = append(expanded, candidate)
			}
		}
	}
	return compactUniqueStrings(expanded)
}

func defaultImageWorkbenchModels(platform string) ([]string, map[string]string) {
	labels := map[string]string{}
	var ids []string
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case PlatformOpenAI:
		for _, model := range openai.DefaultModels {
			if isImageWorkbenchModel(platform, model.ID) {
				ids = append(ids, model.ID)
				labels[model.ID] = model.DisplayName
			}
		}
	case PlatformGemini:
		for _, model := range geminicli.DefaultModels {
			if isImageWorkbenchModel(platform, model.ID) {
				ids = append(ids, model.ID)
				labels[model.ID] = model.DisplayName
			}
		}
	case PlatformGrok:
		for _, model := range xai.DefaultModels() {
			if isImageWorkbenchModel(platform, model.ID) {
				ids = append(ids, model.ID)
				labels[model.ID] = model.DisplayName
			}
		}
	}
	return compactUniqueStrings(ids), labels
}

func isImageWorkbenchModel(platform, model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case PlatformOpenAI:
		return strings.HasPrefix(model, "gpt-image-")
	case PlatformGemini:
		return isImageGenerationModel(model)
	case PlatformGrok:
		return model == "grok-imagine" || model == "grok-imagine-edit" || strings.HasPrefix(model, "grok-imagine-image")
	default:
		return false
	}
}

func mergeImageWorkbenchModelIDs(primary, secondary []string) []string {
	return compactUniqueStrings(append(append([]string(nil), primary...), secondary...))
}

func compactUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func finalizeImageWorkbenchCapabilities(capabilities *ImageWorkbenchCapabilities) *ImageWorkbenchCapabilities {
	if capabilities == nil {
		return nil
	}
	capabilities.CapabilityVersion = ""
	payload, _ := json.Marshal(struct {
		Schema       string                      `json:"schema"`
		Capabilities *ImageWorkbenchCapabilities `json:"capabilities"`
	}{Schema: ImageWorkbenchCapabilitySchemaVersion, Capabilities: capabilities})
	sum := sha256.Sum256(payload)
	capabilities.CapabilityVersion = hex.EncodeToString(sum[:8])
	return capabilities
}
