package service

import (
	"context"
	"time"
)

// Optional durable extension. The encrypted envelope never appears in task APIs.
type AsyncImageResultFetchRepository interface {
	SaveAsyncImageResultFetch(context.Context, AsyncImageTaskTransition, []byte, time.Time) (*AsyncImageTask, error)
	GetAsyncImageResultFetch(context.Context, string) (*AsyncImageResultFetch, error)
	BeginAsyncImageResultFetchAttempt(context.Context, string, int, int) (int, error)
	SaveAsyncImageFetchedObject(context.Context, AsyncImageStagingObject) error
}

type AsyncImageResultFetch struct {
	EncryptedPayload []byte
	Attempts         map[int]int
	ExpiresAt        time.Time
}
