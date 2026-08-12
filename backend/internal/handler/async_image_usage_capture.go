package handler

import (
	"context"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type asyncImageUsageCaptureContextKey struct{}

// AsyncImageUsageCapture receives the exact usage input assembled by the
// existing synchronous handler. The durable worker can then prepare and apply
// billing synchronously without duplicating pricing or account-attribution
// rules. A normal request never carries this context value.
type AsyncImageUsageCapture struct {
	mu     sync.RWMutex
	gemini *service.RecordUsageInput
	openAI *service.OpenAIRecordUsageInput
}

func withAsyncImageUsageCapture(ctx context.Context, capture *AsyncImageUsageCapture) context.Context {
	if capture == nil {
		return ctx
	}
	return context.WithValue(ctx, asyncImageUsageCaptureContextKey{}, capture)
}

func asyncImageUsageCaptureFromContext(ctx context.Context) *AsyncImageUsageCapture {
	if ctx == nil {
		return nil
	}
	capture, _ := ctx.Value(asyncImageUsageCaptureContextKey{}).(*AsyncImageUsageCapture)
	return capture
}

func (c *AsyncImageUsageCapture) setGemini(input *service.RecordUsageInput) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.gemini = input
	c.mu.Unlock()
}

func (c *AsyncImageUsageCapture) setOpenAI(input *service.OpenAIRecordUsageInput) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.openAI = input
	c.mu.Unlock()
}

func (c *AsyncImageUsageCapture) Gemini() *service.RecordUsageInput {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.gemini
}

func (c *AsyncImageUsageCapture) OpenAI() *service.OpenAIRecordUsageInput {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.openAI
}
