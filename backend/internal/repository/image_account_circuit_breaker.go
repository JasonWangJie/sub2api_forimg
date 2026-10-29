package repository

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const imageCircuitKeyPrefix = "image:circuit:"

var imageCircuitFailureScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
redis.call('EXPIRE', KEYS[1], ARGV[2])
redis.call('RPUSH', KEYS[3], 1)
redis.call('LTRIM', KEYS[3], -tonumber(ARGV[3]), -1)
redis.call('EXPIRE', KEYS[3], ARGV[2])
local samples = redis.call('LRANGE', KEYS[3], 0, -1)
local failed = 0
for _,v in ipairs(samples) do failed = failed + tonumber(v) end
if count >= tonumber(ARGV[1]) or (#samples >= tonumber(ARGV[3]) and failed * 2 >= #samples) then
 redis.call('SET', KEYS[2], '1', 'EX', ARGV[2])
 redis.call('SET', KEYS[4], '1', 'EX', ARGV[4])
 return {count, 1}
end
return {count, 0}
`)

var imageCircuitSuccessScript = redis.NewScript(`
redis.call('DEL', KEYS[1])
redis.call('RPUSH', KEYS[2], 0)
redis.call('LTRIM', KEYS[2], -tonumber(ARGV[1]), -1)
redis.call('EXPIRE', KEYS[2], ARGV[2])
return 1
`)

var imageCircuitProbeScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 or redis.call('EXISTS', KEYS[4]) == 1 then return 0 end
if redis.call('EXISTS', KEYS[2]) == 0 then return 1 end
if redis.call('SET', KEYS[3], ARGV[1], 'NX', 'EX', ARGV[2]) then return 2 end
return 0
`)

var imageCircuitFinishProbeScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
if ARGV[3] == '1' then
 redis.call('SET', KEYS[2], '1', 'EX', ARGV[4])
 redis.call('SET', KEYS[3], '1', 'EX', ARGV[5])
end
redis.call('DEL', KEYS[1])
if ARGV[2] == '1' and redis.call('EXISTS', KEYS[2]) == 0 then
 redis.call('DEL', KEYS[3], KEYS[4], KEYS[5])
end
return 1
`)

var imageCircuitRenewProbeScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
redis.call('EXPIRE', KEYS[1], ARGV[2])
return 1
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
	value, err := b.rdb.Exists(ctx, openKey, openKey+":probe", openKey+":capability").Result()
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
	window := max(10, min(100, 2*cfg.ImageCircuitBreakerFailureThreshold))
	values, err := imageCircuitFailureScript.Run(ctx, b.rdb, []string{failuresKey, openKey, openKey + ":samples", openKey + ":tripped"}, cfg.ImageCircuitBreakerFailureThreshold, cfg.ImageCircuitBreakerCooldownSeconds, window, max(3600, 6*cfg.ImageCircuitBreakerCooldownSeconds)).Result()
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
	cfg, enabled := b.config(ctx)
	if !enabled || accountID <= 0 || scope == "" {
		return nil
	}
	failuresKey, openKey := imageCircuitKeys(accountID, scope)
	// A late successful in-flight request must not undo a newer open circuit.
	return imageCircuitSuccessScript.Run(ctx, b.rdb, []string{failuresKey, openKey + ":samples"}, max(10, min(100, 2*cfg.ImageCircuitBreakerFailureThreshold)), cfg.ImageCircuitBreakerCooldownSeconds).Err()
}

func (b *imageAccountCircuitBreaker) AcquireProbe(ctx context.Context, accountID int64, scope string) (string, bool, error) {
	cfg, enabled := b.config(ctx)
	if !enabled || accountID <= 0 || scope == "" {
		return "", true, nil
	}
	_, key := imageCircuitKeys(accountID, scope)
	token := uuid.NewString()
	ttl := max(30, cfg.AccountAttemptTimeoutSeconds+30)
	result, err := imageCircuitProbeScript.Run(ctx, b.rdb, []string{key, key + ":tripped", key + ":probe", key + ":capability"}, token, ttl).Int()
	if err != nil {
		return "", true, err
	}
	if result == 2 {
		return token, true, nil
	}
	return "", result == 1, nil
}

func (b *imageAccountCircuitBreaker) FinishProbe(ctx context.Context, accountID int64, scope, token string, success bool) error {
	return b.FinishProbeResult(ctx, accountID, scope, token, success, false)
}

func (b *imageAccountCircuitBreaker) RenewProbe(ctx context.Context, accountID int64, scope, token string) (bool, error) {
	cfg, enabled := b.config(ctx)
	if !enabled || accountID <= 0 || scope == "" || token == "" {
		return false, nil
	}
	_, key := imageCircuitKeys(accountID, scope)
	owned, err := imageCircuitRenewProbeScript.Run(ctx, b.rdb, []string{key + ":probe"}, token, max(30, cfg.AccountAttemptTimeoutSeconds+30)).Int()
	return owned == 1, err
}

// FinishProbeResult reopens a failed half-open health probe atomically before
// releasing its lock. Final health reporting records the sample once separately.
func (b *imageAccountCircuitBreaker) FinishProbeResult(ctx context.Context, accountID int64, scope, token string, success, healthFailure bool) error {
	if token == "" {
		return nil
	}
	failures, key := imageCircuitKeys(accountID, scope)
	value := "0"
	if success {
		value = "1"
	}
	failure := "0"
	cfg, enabled := b.config(ctx)
	if healthFailure && enabled {
		failure = "1"
	}
	return imageCircuitFinishProbeScript.Run(ctx, b.rdb, []string{key + ":probe", key, key + ":tripped", failures, key + ":samples"}, token, value, failure, max(1, cfg.ImageCircuitBreakerCooldownSeconds), max(3600, 6*cfg.ImageCircuitBreakerCooldownSeconds)).Err()
}

func (b *imageAccountCircuitBreaker) RecordCapabilityFailure(ctx context.Context, accountID int64, scope string) error {
	if _, enabled := b.config(ctx); !enabled || accountID <= 0 || scope == "" {
		return nil
	}
	_, key := imageCircuitKeys(accountID, scope)
	return b.rdb.Set(ctx, key+":capability", "1", 5*time.Minute).Err()
}
