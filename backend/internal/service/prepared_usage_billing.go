package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

type usageBillingPreparationContextKey struct{}

// PreparedUsageBilling is the immutable output of the existing RecordUsage
// calculation path. Persisting it lets an async worker retry the atomic Apply
// operation without resolving prices, peak multipliers, or group overrides a
// second time.
type PreparedUsageBilling struct {
	Command               UsageBillingCommand `json:"command"`
	UsageLog              UsageLog            `json:"usage_log"`
	Cost                  CostBreakdown       `json:"cost"`
	IsSubscriptionBilling bool                `json:"is_subscription_billing"`
	AccountRateMultiplier float64             `json:"account_rate_multiplier"`
	Platform              string              `json:"platform"`
	NotBillable           bool                `json:"not_billable,omitempty"`
}

func (p *PreparedUsageBilling) ActualCost() float64 {
	if p == nil {
		return 0
	}
	return p.Cost.ActualCost
}

type usageBillingPreparationSink struct {
	mu       sync.RWMutex
	prepared *PreparedUsageBilling
}

func withUsageBillingPreparation(ctx context.Context, sink *usageBillingPreparationSink) context.Context {
	return context.WithValue(ctx, usageBillingPreparationContextKey{}, sink)
}

func usageBillingPreparationFromContext(ctx context.Context) *usageBillingPreparationSink {
	if ctx == nil {
		return nil
	}
	sink, _ := ctx.Value(usageBillingPreparationContextKey{}).(*usageBillingPreparationSink)
	return sink
}

func isUsageBillingPreparation(ctx context.Context) bool {
	return usageBillingPreparationFromContext(ctx) != nil
}

func (s *usageBillingPreparationSink) set(prepared *PreparedUsageBilling) {
	if s == nil || prepared == nil {
		return
	}
	s.mu.Lock()
	s.prepared = prepared
	s.mu.Unlock()
}

func (s *usageBillingPreparationSink) get() *PreparedUsageBilling {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.prepared
}

func prepareUsageBilling(ctx context.Context, record func(context.Context) error) (*PreparedUsageBilling, error) {
	if record == nil {
		return nil, errors.New("usage billing record function is required")
	}
	sink := &usageBillingPreparationSink{}
	if err := record(withUsageBillingPreparation(ctx, sink)); err != nil {
		return nil, err
	}
	prepared := sink.get()
	if prepared == nil {
		return nil, errors.New("usage billing preparation produced no billing command")
	}
	return prepared, nil
}

func capturePreparedUsageBilling(ctx context.Context, command *UsageBillingCommand, usageLog *UsageLog, p *postUsageBillingParams) bool {
	return capturePreparedUsageBillingMode(ctx, command, usageLog, p, false)
}

func capturePreparedNotBillableUsageBilling(ctx context.Context, requestID string, usageLog *UsageLog, p *postUsageBillingParams) bool {
	if p == nil {
		return false
	}
	zeroCost := &CostBreakdown{}
	zeroParams := *p
	zeroParams.Cost = zeroCost
	command := buildUsageBillingCommand(requestID, usageLog, &zeroParams)
	return capturePreparedUsageBillingMode(ctx, command, usageLog, &zeroParams, true)
}

func capturePreparedUsageBillingMode(ctx context.Context, command *UsageBillingCommand, usageLog *UsageLog, p *postUsageBillingParams, notBillable bool) bool {
	sink := usageBillingPreparationFromContext(ctx)
	if sink == nil {
		return false
	}
	if command == nil || usageLog == nil || p == nil || p.Cost == nil {
		return false
	}
	commandCopy := *command
	commandCopy.Normalize()
	usageLogCopy := *usageLog
	costCopy := *p.Cost
	sink.set(&PreparedUsageBilling{
		Command:               commandCopy,
		UsageLog:              usageLogCopy,
		Cost:                  costCopy,
		IsSubscriptionBilling: p.IsSubscriptionBill,
		AccountRateMultiplier: p.AccountRateMultiplier,
		Platform:              p.Platform,
		NotBillable:           notBillable,
	})
	return true
}

func applyPreparedUsageBilling(
	ctx context.Context,
	prepared *PreparedUsageBilling,
	p *postUsageBillingParams,
	deps *billingDeps,
	repo UsageBillingRepository,
	usageLogRepo UsageLogRepository,
	component string,
) error {
	if prepared == nil {
		return errors.New("prepared usage billing is required")
	}
	command := prepared.Command
	command.Normalize()
	if command.RequestID == "" {
		return ErrUsageBillingRequestIDRequired
	}
	billingCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	if prepared.NotBillable {
		usageLog := prepared.UsageLog
		if usageLog.RequestID == "" {
			usageLog.RequestID = command.RequestID
		}
		if err := writePreparedUsageLogRequired(billingCtx, usageLogRepo, &usageLog); err != nil {
			return fmt.Errorf("persist prepared usage log: %w", err)
		}
		if p != nil && p.Account != nil && deps != nil && deps.deferredService != nil {
			deps.deferredService.ScheduleLastUsedUpdate(p.Account.ID)
		}
		return nil
	}
	if repo == nil {
		return errors.New("usage billing repository is unavailable")
	}
	result, err := repo.Apply(billingCtx, &command)
	if err != nil {
		return err
	}
	if result != nil && result.Applied {
		if p == nil || deps == nil {
			return errors.New("prepared usage billing apply context is incomplete")
		}
		finalizePostUsageBilling(billingCtx, p, deps, result)
	} else if p != nil && p.Account != nil && deps != nil && deps.deferredService != nil {
		deps.deferredService.ScheduleLastUsedUpdate(p.Account.ID)
	}
	usageLog := prepared.UsageLog
	if usageLog.RequestID == "" {
		usageLog.RequestID = command.RequestID
	}
	if err := writePreparedUsageLogRequired(billingCtx, usageLogRepo, &usageLog); err != nil {
		return fmt.Errorf("persist prepared usage log: %w", err)
	}
	return nil
}

func writePreparedUsageLogRequired(ctx context.Context, repo UsageLogRepository, usageLog *UsageLog) error {
	if repo == nil || usageLog == nil {
		return errors.New("usage log repository is unavailable")
	}
	_, err := repo.Create(ctx, usageLog)
	return err
}

func (s *GatewayService) PrepareRecordUsage(ctx context.Context, input *RecordUsageInput) (*PreparedUsageBilling, error) {
	if s == nil {
		return nil, errors.New("gateway service is unavailable")
	}
	return prepareUsageBilling(ctx, func(prepareCtx context.Context) error {
		return s.RecordUsage(prepareCtx, input)
	})
}

func (s *OpenAIGatewayService) PrepareRecordUsage(ctx context.Context, input *OpenAIRecordUsageInput) (*PreparedUsageBilling, error) {
	if s == nil {
		return nil, errors.New("OpenAI gateway service is unavailable")
	}
	return prepareUsageBilling(ctx, func(prepareCtx context.Context) error {
		return s.RecordUsage(prepareCtx, input)
	})
}

func (s *GatewayService) ApplyPreparedRecordUsage(ctx context.Context, prepared *PreparedUsageBilling, input *RecordUsageInput) error {
	if s == nil || input == nil || input.APIKey == nil || input.User == nil || input.Account == nil {
		return errors.New("gateway prepared usage input is incomplete")
	}
	p := &postUsageBillingParams{
		Cost:                  &prepared.Cost,
		User:                  input.User,
		APIKey:                input.APIKey,
		Account:               input.Account,
		Subscription:          input.Subscription,
		RequestPayloadHash:    prepared.Command.RequestPayloadHash,
		IsSubscriptionBill:    prepared.IsSubscriptionBilling,
		AccountRateMultiplier: prepared.AccountRateMultiplier,
		APIKeyService:         input.APIKeyService,
		Platform:              prepared.Platform,
	}
	return applyPreparedUsageBilling(ctx, prepared, p, s.billingDeps(), s.usageBillingRepo, s.usageLogRepo, "service.gateway.async_image")
}

func (s *OpenAIGatewayService) ApplyPreparedRecordUsage(ctx context.Context, prepared *PreparedUsageBilling, input *OpenAIRecordUsageInput) error {
	if s == nil || input == nil || input.APIKey == nil || input.User == nil || input.Account == nil {
		return errors.New("OpenAI prepared usage input is incomplete")
	}
	p := &postUsageBillingParams{
		Cost:                  &prepared.Cost,
		User:                  input.User,
		APIKey:                input.APIKey,
		Account:               input.Account,
		Subscription:          input.Subscription,
		RequestPayloadHash:    prepared.Command.RequestPayloadHash,
		IsSubscriptionBill:    prepared.IsSubscriptionBilling,
		AccountRateMultiplier: prepared.AccountRateMultiplier,
		APIKeyService:         input.APIKeyService,
		Platform:              prepared.Platform,
	}
	return applyPreparedUsageBilling(ctx, prepared, p, s.billingDeps(), s.usageBillingRepo, s.usageLogRepo, "service.openai_gateway.async_image")
}

func ValidatePreparedUsageBilling(prepared *PreparedUsageBilling, taskID string, apiKeyID int64) error {
	if prepared == nil {
		return errors.New("prepared usage billing is missing")
	}
	wantRequestID := "client:async-image:" + taskID
	if prepared.Command.RequestID != wantRequestID {
		return fmt.Errorf("prepared usage request id mismatch: got %q, want %q", prepared.Command.RequestID, wantRequestID)
	}
	if prepared.Command.APIKeyID != apiKeyID {
		return errors.New("prepared usage API key does not match task owner")
	}
	return nil
}
