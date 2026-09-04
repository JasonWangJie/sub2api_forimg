package repository

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const imageCircuitKeyPrefix = "image:circuit:"

var imageCircuitFailureScript = redis.NewScript(`
local failures = KEYS[1]
local opened = KEYS[2]
local threshold = tonumber(ARGV[1])
local cooldown = tonumber(ARGV[2])
local count = redis.call('INCR', failures)
redis.call('EXPIRE', failures, cooldown)
if count >= threshold then
  redis.call('SET', opened, '1', 'EX', cooldown)
  return {count, 1}
end
return {count, 0}
`)

type imageAccountCircuitBreaker struct {
	rdb      *redis.Client
	provider service.ImageCircuitBreakerSettingsProvider
}

func NewImageAccountCircuitBreaker(rdb *redis.Client) service.ImageAccountCircuitBreaker {
	return &imageAccountCircuitBreaker{rdb: rdb}
}

func ConfigureImageAccountCircuitBreaker(b service.ImageAccountCircuitBreaker, provider service.ImageCircuitBreakerSettingsProvider) {
	if configurable, ok := b.(*imageAccountCircuitBreaker); ok {
		configurable.provider = provider
	}
}

func (b *imageAccountCircuitBreaker) config(ctx context.Context) (service.AsyncImageRuntimeConfig, bool) {
	if b == nil || b.rdb == nil || b.provider == nil {
		return service.AsyncImageRuntimeConfig{}, false
	}
	cfg, err := b.provider.RuntimeConfig(ctx)
	if err != nil || !cfg.ImageCircuitBreakerEnabled {
		return cfg, false
	}
	return cfg, cfg.ImageCircuitBreakerFailureThreshold > 0 && cfg.ImageCircuitBreakerCooldownSeconds > 0
}

func imageCircuitKeys(accountID int64, scope string) (string, string) {
	base := fmt.Sprintf("%s%s:%d", imageCircuitKeyPrefix, scope, accountID)
	return base + ":failures", base + ":open"
}

func (b *imageAccountCircuitBreaker) IsOpen(ctx context.Context, accountID int64, scope string) (bool, error) {
	if _, enabled := b.config(ctx); !enabled || accountID <= 0 || scope == "" {
		return false, nil
	}
	_, openKey := imageCircuitKeys(accountID, scope)
	value, err := b.rdb.Exists(ctx, openKey).Result()
	if err != nil {
		slog.Warn("image circuit breaker read failed", "account_id", accountID, "scope", scope, "error", err)
		return false, err
	}
	return value > 0, nil
}

func (b *imageAccountCircuitBreaker) RecordFailure(ctx context.Context, accountID int64, scope string) (bool, error) {
	cfg, enabled := b.config(ctx)
	if !enabled || accountID <= 0 || scope == "" {
		return false, nil
	}
	failuresKey, openKey := imageCircuitKeys(accountID, scope)
	values, err := imageCircuitFailureScript.Run(ctx, b.rdb, []string{failuresKey, openKey}, cfg.ImageCircuitBreakerFailureThreshold, cfg.ImageCircuitBreakerCooldownSeconds).Result()
	if err != nil {
		return false, err
	}
	list, ok := values.([]interface{})
	if !ok || len(list) < 2 {
		return false, fmt.Errorf("unexpected image circuit breaker response %T", values)
	}
	opened, _ := redisInt64(list[1])
	return opened == 1, nil
}

func redisInt64(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case string:
		var parsed int64
		_, err := fmt.Sscan(n, &parsed)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func (b *imageAccountCircuitBreaker) RecordSuccess(ctx context.Context, accountID int64, scope string) error {
	if _, enabled := b.config(ctx); !enabled || accountID <= 0 || scope == "" {
		return nil
	}
	failuresKey, openKey := imageCircuitKeys(accountID, scope)
	return b.rdb.Del(ctx, failuresKey, openKey).Err()
}
