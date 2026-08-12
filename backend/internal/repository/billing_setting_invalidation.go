package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const billingChargeMultiplierChannel = "settings:billing_charge_multiplier"

type billingSettingInvalidation struct {
	rdb *redis.Client
}

func NewBillingSettingInvalidation(rdb *redis.Client) service.BillingSettingInvalidation {
	return &billingSettingInvalidation{rdb: rdb}
}

func (i *billingSettingInvalidation) PublishBillingChargeMultiplier(ctx context.Context, value string) error {
	if i == nil || i.rdb == nil {
		return errors.New("billing setting invalidation is not configured")
	}
	return i.rdb.Publish(ctx, billingChargeMultiplierChannel, value).Err()
}

func (i *billingSettingInvalidation) SubscribeBillingChargeMultiplier(ctx context.Context, handler func(value string)) error {
	if i == nil || i.rdb == nil {
		return errors.New("billing setting invalidation is not configured")
	}
	pubsub := i.rdb.Subscribe(ctx, billingChargeMultiplierChannel)
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return fmt.Errorf("subscribe to billing setting invalidation: %w", err)
	}
	defer func() { _ = pubsub.Close() }()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case message, ok := <-pubsub.Channel():
			if !ok {
				return errors.New("billing setting invalidation channel closed")
			}
			if message != nil && handler != nil {
				handler(message.Payload)
			}
		}
	}
}

var _ service.BillingSettingInvalidation = (*billingSettingInvalidation)(nil)
