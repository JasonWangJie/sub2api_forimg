package service

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tidwall/sjson"
)

// NormalizeAsyncOutputOptions validates the async output contract before a task
// is created and freezes defaults in its persisted body. Older static-image
// models keep their existing passthrough behavior.
func (r *OpenAIImagesRequest) NormalizeAsyncOutputOptions() ([]byte, string, error) {
	if r == nil {
		return nil, "", fmt.Errorf("parsed images request is required")
	}
	model := strings.ToLower(strings.TrimSpace(r.Model))
	extended := model == "gpt-image-2.6-flare" || model == "gpt-image-2.6-sunburst"
	format := strings.ToLower(strings.TrimSpace(r.OutputFormat))
	if !extended {
		if format == "gif" || format == "svg" || r.FrameCount != nil || r.FrameDurationMS != nil || r.SVG != nil {
			return nil, "", fmt.Errorf("GIF/SVG output and frame options require gpt-image-2.6-flare or gpt-image-2.6-sunburst")
		}
		return r.Body, r.ContentType, nil
	}
	if format == "" {
		format = "png"
	}
	switch format {
	case "png", "webp", "jpeg", "gif", "svg":
	default:
		return nil, "", fmt.Errorf("output_format must be png, webp, jpeg, gif or svg")
	}
	if format != "gif" && (r.FrameCount != nil || r.FrameDurationMS != nil) {
		return nil, "", fmt.Errorf("frame_count and frame_duration_ms require output_format=gif")
	}
	if r.SVG != nil && format != "svg" {
		return nil, "", fmt.Errorf("svg options require output_format=svg")
	}
	fields := map[string]string{"output_format": format}
	if format == "gif" {
		count, duration := 9, 200
		if r.FrameCount != nil {
			count = *r.FrameCount
		}
		if r.FrameDurationMS != nil {
			duration = *r.FrameDurationMS
		}
		if count < 2 || count > 16 {
			return nil, "", fmt.Errorf("frame_count must be an integer between 2 and 16")
		}
		if duration < 20 || duration > 2000 {
			return nil, "", fmt.Errorf("frame_duration_ms must be an integer between 20 and 2000")
		}
		r.FrameCount, r.FrameDurationMS = &count, &duration
		fields["frame_count"] = strconv.Itoa(count)
		fields["frame_duration_ms"] = strconv.Itoa(duration)
	}
	r.OutputFormat = format
	if r.Multipart {
		return rewriteOpenAIImagesMultipartFields(r.Body, r.ContentType, fields, nil)
	}
	body := r.Body
	for key, value := range fields {
		var err error
		if key == "output_format" {
			body, err = sjson.SetBytes(body, key, value)
		} else {
			number, _ := strconv.Atoi(value)
			body, err = sjson.SetBytes(body, key, number)
		}
		if err != nil {
			return nil, "", fmt.Errorf("normalize async image output: %w", err)
		}
	}
	return body, r.ContentType, nil
}
