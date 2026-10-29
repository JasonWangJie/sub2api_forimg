package handler

import (
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type asyncImageOutputDiagnosticsKey struct{}

// Describe response shape, never text, signed URLs, base64, or prompt content.
func asyncImageOutputStructure(body []byte, capture *service.GeminiImageResponseCapture) json.RawMessage {
	if capture != nil && len(capture.RawResponse()) > 0 {
		body = capture.RawResponse()
	}
	var root map[string]any
	summary := map[string]any{"stage": "output_parse", "error_code": "upstream_invalid_output", "valid_json": json.Unmarshal(body, &root) == nil}
	counts := map[string]int{}
	finishes := map[string]int{}
	var walk func(any)
	walk = func(v any) {
		switch n := v.(type) {
		case map[string]any:
			for k, x := range n {
				switch k {
				case "inlineData", "inline_data":
					counts["inline"]++
				case "fileData", "file_data":
					counts["file"]++
				case "url", "fileUri", "file_uri":
					counts["url"]++
				case "b64_json":
					counts["base64"]++
				case "text":
					counts["text"]++
				case "functionCall", "function_call", "tool_calls":
					counts["tool"]++
				case "finishReason", "finish_reason":
					if val, ok := x.(string); ok {
						switch val {
						case "STOP", "MAX_TOKENS", "SAFETY", "RECITATION", "OTHER", "IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_OTHER", "NO_IMAGE", "stop", "length", "content_filter", "tool_calls":
							finishes[val]++
						default:
							finishes["unrecognized"]++
						}
					}
				}
				walk(x)
			}
		case []any:
			for _, x := range n {
				walk(x)
			}
		}
	}
	walk(root)
	summary["parts"] = counts
	summary["finish_reasons"] = finishes
	encoded, _ := json.Marshal(summary)
	return encoded
}
