package service

import (
	"context"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

// ImageResolutionExpectation records what was actually sent upstream. It is
// deliberately independent of image billing, whose explicit tariff may differ
// from the decoded result dimensions.
type ImageResolutionExpectation struct {
	RequestedModel   string    `json:"requested_model"`
	UpstreamModel    string    `json:"upstream_model"`
	SizeTier         string    `json:"size_tier"`
	ExpectedWidth    int       `json:"expected_width,omitempty"`
	ExpectedHeight   int       `json:"expected_height,omitempty"`
	ExpectedPixels   int64     `json:"expected_pixels,omitempty"`
	RuleVersion      int       `json:"rule_version"`
	AttemptStartedAt time.Time `json:"attempt_started_at,omitempty"`
}

// Positive dimensions always come from the final image bytes, never response
// size metadata. Zero dimensions represent a result that could not be measured.
type ImageResolutionOutput struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type ImageResolutionGuardObserver interface {
	Observe(context.Context, ImageResolutionObservation) error
	IsCooling(context.Context, int64, string, string) (bool, error)
}

type imageResolutionCaptureKey struct{}
type imageResolutionRequestKey struct{}

// WithImageResolutionRequest keeps the client model before channel/account
// mappings, without changing the account-pool routing policy for Responses.
func WithImageResolutionRequest(ctx context.Context, body []byte, contentType, requestedModel string) context.Context {
	if existing, ok := ctx.Value(imageResolutionRequestKey{}).(ImageResolutionExpectation); ok && existing.RequestedModel != "" {
		return ctx
	}
	e := ResolveImageResolutionExpectation(body, contentType, requestedModel, "", "")
	if e.ExpectedPixels <= 0 {
		imageTool := false
		for _, tool := range gjson.GetBytes(body, "tools").Array() {
			if tool.Get("type").String() == "image_generation" {
				imageTool = true
				break
			}
		}
		if !imageTool {
			return ctx
		}
	}
	return context.WithValue(ctx, imageResolutionRequestKey{}, e)
}

type imageResolutionCapture struct {
	mu          sync.Mutex
	expectation *ImageResolutionExpectation
}

func beginImageResolutionCapture(ctx context.Context) (context.Context, *imageResolutionCapture) {
	capture := &imageResolutionCapture{}
	return context.WithValue(ctx, imageResolutionCaptureKey{}, capture), capture
}

func (c *imageResolutionCapture) get() *ImageResolutionExpectation {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.expectation == nil {
		return nil
	}
	copy := *c.expectation
	return &copy
}

func setImageResolutionCapture(ctx context.Context, expectation ImageResolutionExpectation) {
	if capture, _ := ctx.Value(imageResolutionCaptureKey{}).(*imageResolutionCapture); capture != nil {
		capture.mu.Lock()
		capture.expectation = &expectation
		capture.mu.Unlock()
	}
}
