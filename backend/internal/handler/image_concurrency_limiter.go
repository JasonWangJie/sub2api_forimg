package handler

import "github.com/Wei-Shaw/sub2api/internal/service"

// Keep the historical handler-local name for tests and call sites while the
// implementation is shared by OpenAI and Gemini through ConcurrencyService.
type imageConcurrencyLimiter = service.ImageConcurrencyLimiter
