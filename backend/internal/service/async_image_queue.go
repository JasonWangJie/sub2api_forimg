package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrAsyncImageQueueEmpty      = infraerrors.New(http.StatusNotFound, "ASYNC_IMAGE_QUEUE_EMPTY", "asynchronous image queue is empty")
	ErrAsyncImageAlreadyQueued   = infraerrors.New(http.StatusConflict, "ASYNC_IMAGE_ALREADY_QUEUED", "asynchronous image task is already queued")
	ErrAsyncImageQueueBadPayload = infraerrors.New(http.StatusBadRequest, "ASYNC_IMAGE_QUEUE_BAD_PAYLOAD", "invalid asynchronous image queue payload")
	ErrAsyncImageQueueLeaseLost  = infraerrors.New(http.StatusConflict, "ASYNC_IMAGE_QUEUE_LEASE_LOST", "asynchronous image queue lease is no longer owned by this worker")
)

type AsyncImageQueueReservation struct {
	TaskID     string
	LeaseToken string
}

type AsyncImageQueue interface {
	Enqueue(ctx context.Context, taskID string) error
	Reserve(ctx context.Context, blockTimeout time.Duration) (*AsyncImageQueueReservation, error)
	Ack(ctx context.Context, reservation *AsyncImageQueueReservation) error
	Heartbeat(ctx context.Context, reservation *AsyncImageQueueReservation) error
	RequeueAfter(ctx context.Context, reservation *AsyncImageQueueReservation, delay time.Duration) error
	MoveDueDelayedToReady(ctx context.Context, limit int) (int, error)
	RecoverStaleActive(ctx context.Context, staleAfter time.Duration, limit int) (int, error)
}

func IsValidAsyncImageTaskID(taskID string) bool {
	value := strings.TrimSpace(taskID)
	return strings.HasPrefix(value, "asyncimg_") && len(value) > len("asyncimg_")
}

func IsAsyncImageQueueAlreadyQueued(err error) bool {
	return errors.Is(err, ErrAsyncImageAlreadyQueued)
}
