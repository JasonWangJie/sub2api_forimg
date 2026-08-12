package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	asyncImageReadyKey       = "async_image:queue:ready"
	asyncImageDelayedKey     = "async_image:queue:delayed"
	asyncImageActiveKey      = "async_image:queue:active"
	asyncImageLeasesKey      = "async_image:queue:leases"
	asyncImageInflightPrefix = "async_image:queue:inflight:"
	asyncImageInflightTTL    = 48 * time.Hour
)

var asyncImageEnqueueScript = redis.NewScript(`
if redis.call("SET", KEYS[1], ARGV[1], "NX", "PX", ARGV[2]) then
  redis.call("LPUSH", KEYS[2], ARGV[1])
  return 1
end
return 0
`)

var asyncImageReserveScript = redis.NewScript(`
local task = redis.call("RPOP", KEYS[1])
if not task then return nil end
redis.call("ZADD", KEYS[2], ARGV[1], task)
redis.call("HSET", KEYS[3], task, ARGV[2])
redis.call("PEXPIRE", KEYS[4] .. task, ARGV[3])
return task
`)

var asyncImageAckScript = redis.NewScript(`
if redis.call("HGET", KEYS[2], ARGV[1]) ~= ARGV[2] then return 0 end
redis.call("ZREM", KEYS[1], ARGV[1])
redis.call("HDEL", KEYS[2], ARGV[1])
redis.call("DEL", KEYS[3])
return 1
`)

var asyncImageHeartbeatScript = redis.NewScript(`
if redis.call("HGET", KEYS[2], ARGV[1]) ~= ARGV[2] then return 0 end
if not redis.call("ZSCORE", KEYS[1], ARGV[1]) then return 0 end
redis.call("ZADD", KEYS[1], ARGV[3], ARGV[1])
redis.call("PEXPIRE", KEYS[3], ARGV[4])
return 1
`)

var asyncImageRequeueScript = redis.NewScript(`
if redis.call("HGET", KEYS[2], ARGV[1]) ~= ARGV[2] then return 0 end
redis.call("ZREM", KEYS[1], ARGV[1])
redis.call("HDEL", KEYS[2], ARGV[1])
redis.call("ZADD", KEYS[3], ARGV[3], ARGV[1])
redis.call("PEXPIRE", KEYS[4], ARGV[4])
return 1
`)

var asyncImageMoveDelayedScript = redis.NewScript(`
local tasks = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", ARGV[1], "LIMIT", 0, ARGV[2])
for _, task in ipairs(tasks) do
  redis.call("ZREM", KEYS[1], task)
  redis.call("LPUSH", KEYS[2], task)
end
return #tasks
`)

var asyncImageRecoverActiveScript = redis.NewScript(`
local tasks = redis.call("ZRANGEBYSCORE", KEYS[1], "-inf", ARGV[1], "LIMIT", 0, ARGV[2])
for _, task in ipairs(tasks) do
  redis.call("ZREM", KEYS[1], task)
  redis.call("HDEL", KEYS[3], task)
  redis.call("LPUSH", KEYS[2], task)
end
return #tasks
`)

type asyncImageQueue struct {
	rdb *redis.Client
}

func NewAsyncImageQueue(rdb *redis.Client) service.AsyncImageQueue {
	return &asyncImageQueue{rdb: rdb}
}

func (q *asyncImageQueue) Enqueue(ctx context.Context, taskID string) error {
	if q == nil || q.rdb == nil || !service.IsValidAsyncImageTaskID(taskID) {
		return service.ErrAsyncImageQueueBadPayload
	}
	applied, err := asyncImageEnqueueScript.Run(ctx, q.rdb,
		[]string{asyncImageInflightPrefix + taskID, asyncImageReadyKey},
		taskID, asyncImageInflightTTL.Milliseconds(),
	).Int()
	if err != nil {
		return err
	}
	if applied == 0 {
		return service.ErrAsyncImageAlreadyQueued
	}
	return nil
}

func (q *asyncImageQueue) Reserve(ctx context.Context, blockTimeout time.Duration) (*service.AsyncImageQueueReservation, error) {
	if q == nil || q.rdb == nil {
		return nil, service.ErrAsyncImageQueueBadPayload
	}
	leaseToken := uuid.NewString()
	deadline := time.Now().Add(blockTimeout)
	for {
		raw, err := asyncImageReserveScript.Run(ctx, q.rdb,
			[]string{asyncImageReadyKey, asyncImageActiveKey, asyncImageLeasesKey, asyncImageInflightPrefix},
			time.Now().UnixMilli(), leaseToken, asyncImageInflightTTL.Milliseconds(),
		).Result()
		if err == nil {
			taskID, ok := raw.(string)
			if !ok || !service.IsValidAsyncImageTaskID(taskID) {
				return nil, service.ErrAsyncImageQueueBadPayload
			}
			return &service.AsyncImageQueueReservation{TaskID: taskID, LeaseToken: leaseToken}, nil
		}
		if !errors.Is(err, redis.Nil) {
			return nil, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, service.ErrAsyncImageQueueEmpty
		}
		wait := time.Second
		if remaining < wait {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (q *asyncImageQueue) Ack(ctx context.Context, reservation *service.AsyncImageQueueReservation) error {
	if !validAsyncImageReservation(q, reservation) {
		return service.ErrAsyncImageQueueBadPayload
	}
	applied, err := asyncImageAckScript.Run(ctx, q.rdb,
		[]string{asyncImageActiveKey, asyncImageLeasesKey, asyncImageInflightPrefix + reservation.TaskID},
		reservation.TaskID, reservation.LeaseToken,
	).Int()
	return asyncImageLeaseResult(applied, err)
}

func (q *asyncImageQueue) Heartbeat(ctx context.Context, reservation *service.AsyncImageQueueReservation) error {
	if !validAsyncImageReservation(q, reservation) {
		return service.ErrAsyncImageQueueBadPayload
	}
	applied, err := asyncImageHeartbeatScript.Run(ctx, q.rdb,
		[]string{asyncImageActiveKey, asyncImageLeasesKey, asyncImageInflightPrefix + reservation.TaskID},
		reservation.TaskID, reservation.LeaseToken, time.Now().UnixMilli(), asyncImageInflightTTL.Milliseconds(),
	).Int()
	return asyncImageLeaseResult(applied, err)
}

func (q *asyncImageQueue) RequeueAfter(ctx context.Context, reservation *service.AsyncImageQueueReservation, delay time.Duration) error {
	if !validAsyncImageReservation(q, reservation) {
		return service.ErrAsyncImageQueueBadPayload
	}
	if delay < 0 {
		delay = 0
	}
	applied, err := asyncImageRequeueScript.Run(ctx, q.rdb,
		[]string{asyncImageActiveKey, asyncImageLeasesKey, asyncImageDelayedKey, asyncImageInflightPrefix + reservation.TaskID},
		reservation.TaskID, reservation.LeaseToken, time.Now().Add(delay).UnixMilli(), asyncImageInflightTTL.Milliseconds(),
	).Int()
	return asyncImageLeaseResult(applied, err)
}

func (q *asyncImageQueue) MoveDueDelayedToReady(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	return asyncImageMoveDelayedScript.Run(ctx, q.rdb,
		[]string{asyncImageDelayedKey, asyncImageReadyKey}, time.Now().UnixMilli(), limit,
	).Int()
}

func (q *asyncImageQueue) RecoverStaleActive(ctx context.Context, staleAfter time.Duration, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	if staleAfter <= 0 {
		staleAfter = 2 * time.Minute
	}
	return asyncImageRecoverActiveScript.Run(ctx, q.rdb,
		[]string{asyncImageActiveKey, asyncImageReadyKey, asyncImageLeasesKey}, time.Now().Add(-staleAfter).UnixMilli(), limit,
	).Int()
}

func validAsyncImageReservation(q *asyncImageQueue, reservation *service.AsyncImageQueueReservation) bool {
	return q != nil && q.rdb != nil && reservation != nil &&
		service.IsValidAsyncImageTaskID(reservation.TaskID) && strings.TrimSpace(reservation.LeaseToken) != ""
}

func asyncImageLeaseResult(applied int, err error) error {
	if err != nil {
		return err
	}
	if applied != 1 {
		return service.ErrAsyncImageQueueLeaseLost
	}
	return nil
}

var _ service.AsyncImageQueue = (*asyncImageQueue)(nil)
