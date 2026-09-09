package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	billingChargeMultiplierChannel  = "settings:billing_charge_multiplier"
	imageConcurrencySettingsChannel = "settings:image_concurrency"
)

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
	return i.subscribe(ctx, billingChargeMultiplierChannel, "billing setting invalidation", handler)
}

func (i *billingSettingInvalidation) PublishImageConcurrencySettings(ctx context.Context, value string) error {
	if i == nil || i.rdb == nil {
		return errors.New("image concurrency setting invalidation is not configured")
	}
	return i.rdb.Publish(ctx, imageConcurrencySettingsChannel, value).Err()
}

func (i *billingSettingInvalidation) SubscribeImageConcurrencySettings(ctx context.Context, handler func(value string)) error {
	return i.subscribe(ctx, imageConcurrencySettingsChannel, "image concurrency setting invalidation", handler)
}

func (i *billingSettingInvalidation) subscribe(ctx context.Context, channel, description string, handler func(value string)) error {
	if i == nil || i.rdb == nil {
		return fmt.Errorf("%s is not configured", description)
	}
	pubsub := i.rdb.Subscribe(ctx, channel)
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return fmt.Errorf("subscribe to %s: %w", description, err)
	}
	defer func() { _ = pubsub.Close() }()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case message, ok := <-pubsub.Channel():
			if !ok {
				return fmt.Errorf("%s channel closed", description)
			}
			if message != nil && handler != nil {
				handler(message.Payload)
			}
		}
	}
}

var _ service.BillingSettingInvalidation = (*billingSettingInvalidation)(nil)
var _ service.ImageConcurrencySettingInvalidation = (*billingSettingInvalidation)(nil)
