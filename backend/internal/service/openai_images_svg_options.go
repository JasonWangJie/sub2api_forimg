package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

// SVG author selection depends on the upstream project. Keep it optional and
// never substitute the top-level image model for an unspecified author model.
type OpenAIImagesSVGOptions struct {
	Model     string                    `json:"model,omitempty"`
	Reasoning *OpenAIImagesSVGReasoning `json:"reasoning,omitempty"`
}

type OpenAIImagesSVGReasoning struct {
	Effort string `json:"effort,omitempty"`
}

// ParseOpenAIImagesSVGOptions is shared by ingress and the local-reference
// multipart builder so nested author settings cannot disappear during retries.
func ParseOpenAIImagesSVGOptions(raw []byte) (*OpenAIImagesSVGOptions, error) {
	if !gjson.ValidBytes(raw) {
		return nil, fmt.Errorf("svg must be an object")
	}
	value := gjson.ParseBytes(raw)
	if value.Type == gjson.Null {
		return nil, nil
	}
	if !value.IsObject() {
		return nil, fmt.Errorf("svg must be an object")
	}
	if model := value.Get("model"); model.Exists() {
		if model.Type != gjson.String || strings.TrimSpace(model.String()) == "" {
			return nil, fmt.Errorf("svg.model must be a non-empty string")
		}
	}
	if reasoning := value.Get("reasoning"); reasoning.Exists() && reasoning.Type != gjson.Null {
		if !reasoning.IsObject() {
			return nil, fmt.Errorf("svg.reasoning must be an object")
		}
		if effort := reasoning.Get("effort"); effort.Exists() && effort.Type != gjson.Null {
			if effort.Type != gjson.String || !validOpenAIImagesSVGEffort(effort.String()) {
				return nil, fmt.Errorf("invalid svg.reasoning.effort")
			}
		}
	}
	var options OpenAIImagesSVGOptions
	if err := json.Unmarshal(raw, &options); err != nil {
		return nil, fmt.Errorf("invalid svg options: %w", err)
	}
	return &options, nil
}

func validOpenAIImagesSVGEffort(effort string) bool {
	switch effort {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

func setOpenAIImagesSVGMultipartOption(req *OpenAIImagesRequest, name, value string) error {
	if req.SVG == nil {
		req.SVG = &OpenAIImagesSVGOptions{}
	}
	switch name {
	case "svg[model]":
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("svg.model must be a non-empty string")
		}
		if req.SVG.Model != "" {
			return fmt.Errorf("svg[model] must not be repeated")
		}
		req.SVG.Model = value
	case "svg[reasoning][effort]":
		if !validOpenAIImagesSVGEffort(value) {
			return fmt.Errorf("invalid svg.reasoning.effort")
		}
		if req.SVG.Reasoning != nil {
			return fmt.Errorf("svg[reasoning][effort] must not be repeated")
		}
		req.SVG.Reasoning = &OpenAIImagesSVGReasoning{Effort: value}
	}
	return nil
}

func (s *OpenAIImagesSVGOptions) MultipartFields() map[string]string {
	fields := make(map[string]string)
	if s == nil {
		return fields
	}
	if s.Model != "" {
		fields["svg[model]"] = s.Model
	}
	if s.Reasoning != nil && s.Reasoning.Effort != "" {
		fields["svg[reasoning][effort]"] = s.Reasoning.Effort
	}
	return fields
}
