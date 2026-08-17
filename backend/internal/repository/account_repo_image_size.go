package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbgroupimagesizeaccount "github.com/Wei-Shaw/sub2api/ent/groupimagesizeaccount"
	dbpredicate "github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func normalizeImageSizeTierKey(sizeTier string) (string, error) {
	tier := service.NormalizeImageSizePoolTier(sizeTier)
	if !service.IsValidImageSizeTier(tier) {
		return "", fmt.Errorf("invalid image size tier %q", sizeTier)
	}
	return tier, nil
}

// HasImageSizeTierConfigured reports whether the group has any bindings for the tier.
func (r *accountRepository) HasImageSizeTierConfigured(ctx context.Context, groupID int64, sizeTier string) (bool, error) {
	tier, err := normalizeImageSizeTierKey(sizeTier)
	if err != nil {
		return false, err
	}
	count, err := r.client.GroupImageSizeAccount.Query().
		Where(
			dbgroupimagesizeaccount.GroupIDEQ(groupID),
			dbgroupimagesizeaccount.SizeTierEQ(tier),
		).
		Count(ctx)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ListImageSizeAccountIDsByGroupID returns distinct account IDs bound in any size-tier pool.
func (r *accountRepository) ListImageSizeAccountIDsByGroupID(ctx context.Context, groupID int64) ([]int64, error) {
	rows, err := r.client.GroupImageSizeAccount.Query().
		Where(dbgroupimagesizeaccount.GroupIDEQ(groupID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[int64]struct{}, len(rows))
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		if _, ok := seen[row.AccountID]; ok {
			continue
		}
		seen[row.AccountID] = struct{}{}
		ids = append(ids, row.AccountID)
	}
	return ids, nil
}

// ListImageSizeAccounts returns all size-tier bindings for a group ordered by tier then priority.
func (r *accountRepository) ListImageSizeAccounts(ctx context.Context, groupID int64) ([]service.GroupImageSizeAccount, error) {
	rows, err := r.client.GroupImageSizeAccount.Query().
		Where(dbgroupimagesizeaccount.GroupIDEQ(groupID)).
		Order(
			dbgroupimagesizeaccount.BySizeTier(),
			dbgroupimagesizeaccount.ByPriority(),
			dbgroupimagesizeaccount.ByAccountID(),
		).
		WithAccount().
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.GroupImageSizeAccount, 0, len(rows))
	for _, row := range rows {
		item := service.GroupImageSizeAccount{
			ID:        row.ID,
			GroupID:   row.GroupID,
			SizeTier:  row.SizeTier,
			AccountID: row.AccountID,
			Priority:  row.Priority,
			CreatedAt: row.CreatedAt,
		}
		if row.Edges.Account != nil {
			name := row.Edges.Account.Name
			item.AccountName = &name
		}
		out = append(out, item)
	}
	return out, nil
}

// ReplaceImageSizeAccounts replaces all size-tier bindings for a group.
// Omitted or empty tiers clear that tier (fallback to default account_groups pool).
func (r *accountRepository) ReplaceImageSizeAccounts(ctx context.Context, groupID int64, bindings service.GroupImageSizeAccountBindings) error {
	if bindings == nil {
		bindings = service.GroupImageSizeAccountBindings{}
	}

	normalized := make(service.GroupImageSizeAccountBindings, 3)
	for _, tier := range service.ValidImageSizeTiers() {
		entries := bindings[tier]
		dedup := make([]service.GroupImageSizeAccountBinding, 0, len(entries))
		seen := make(map[int64]struct{}, len(entries))
		for i, entry := range entries {
			if entry.AccountID <= 0 {
				return fmt.Errorf("invalid account_id in %s bindings", tier)
			}
			if _, ok := seen[entry.AccountID]; ok {
				continue
			}
			seen[entry.AccountID] = struct{}{}
			priority := entry.Priority
			if priority <= 0 {
				priority = i + 1
			}
			dedup = append(dedup, service.GroupImageSizeAccountBinding{
				AccountID: entry.AccountID,
				Priority:  priority,
			})
		}
		normalized[tier] = dedup
	}
	for key := range bindings {
		if !service.IsValidImageSizeTier(key) {
			return fmt.Errorf("invalid image size tier %q", key)
		}
	}

	tx, err := r.client.Tx(ctx)
	if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
		return err
	}
	var txClient *dbent.Client
	if err == nil {
		defer func() { _ = tx.Rollback() }()
		txClient = tx.Client()
	} else {
		txClient = r.client
	}

	if _, err := txClient.ExecContext(ctx, `DELETE FROM group_image_size_accounts WHERE group_id = $1`, groupID); err != nil {
		return err
	}

	accountIDs := make([]int64, 0)
	for _, tier := range service.ValidImageSizeTiers() {
		for _, entry := range normalized[tier] {
			accountIDs = append(accountIDs, entry.AccountID)
		}
	}
	if len(accountIDs) > 0 {
		uniqueIDs := uniqueImageSizeAccountIDs(accountIDs)
		existing, err := txClient.Account.Query().
			Where(
				dbaccount.IDIn(uniqueIDs...),
				dbaccount.DeletedAtIsNil(),
			).
			All(ctx)
		if err != nil {
			return err
		}
		if len(existing) != len(uniqueIDs) {
			return service.ErrAccountNotFound
		}
	}

	for _, tier := range service.ValidImageSizeTiers() {
		for _, entry := range normalized[tier] {
			if _, err := txClient.GroupImageSizeAccount.Create().
				SetGroupID(groupID).
				SetSizeTier(tier).
				SetAccountID(entry.AccountID).
				SetPriority(entry.Priority).
				Save(ctx); err != nil {
				return err
			}
		}
	}

	if tx != nil {
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// ListSchedulableByGroupImageSizeTier loads schedulable accounts from the size-tier pool.
// Join priority overlays Account.Priority so pool ordering is respected by schedulers.
func (r *accountRepository) ListSchedulableByGroupImageSizeTier(ctx context.Context, groupID int64, sizeTier string, platforms []string) ([]service.Account, error) {
	tier, err := normalizeImageSizeTierKey(sizeTier)
	if err != nil {
		return nil, err
	}

	q := r.client.GroupImageSizeAccount.Query().
		Where(
			dbgroupimagesizeaccount.GroupIDEQ(groupID),
			dbgroupimagesizeaccount.SizeTierEQ(tier),
		)

	preds := make([]dbpredicate.Account, 0, 6)
	preds = append(preds, dbaccount.DeletedAtIsNil())
	preds = append(preds, dbaccount.StatusEQ(service.StatusActive))
	preds = append(preds, dbaccount.SchedulableEQ(true))
	if len(platforms) > 0 {
		preds = append(preds, dbaccount.PlatformIn(platforms...))
	}
	now := time.Now()
	preds = append(preds,
		tempUnschedulablePredicate(),
		notExpiredPredicate(now),
		dbaccount.Or(dbaccount.OverloadUntilIsNil(), dbaccount.OverloadUntilLTE(now)),
		dbaccount.Or(dbaccount.RateLimitResetAtIsNil(), dbaccount.RateLimitResetAtLTE(now)),
	)
	q = q.Where(dbgroupimagesizeaccount.HasAccountWith(preds...))

	rows, err := q.
		Order(
			dbgroupimagesizeaccount.ByPriority(),
			dbgroupimagesizeaccount.ByAccountField(dbaccount.FieldPriority),
		).
		WithAccount().
		All(ctx)
	if err != nil {
		return nil, err
	}

	orderedIDs := make([]int64, 0, len(rows))
	priorityByID := make(map[int64]int, len(rows))
	accountMap := make(map[int64]*dbent.Account, len(rows))
	for _, row := range rows {
		if row.Edges.Account == nil {
			continue
		}
		if _, exists := accountMap[row.AccountID]; exists {
			continue
		}
		accountMap[row.AccountID] = row.Edges.Account
		priorityByID[row.AccountID] = row.Priority
		orderedIDs = append(orderedIDs, row.AccountID)
	}

	accounts := make([]*dbent.Account, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		if acc, ok := accountMap[id]; ok {
			accounts = append(accounts, acc)
		}
	}

	result, err := r.accountsToService(ctx, accounts)
	if err != nil {
		return nil, err
	}
	for i := range result {
		if prio, ok := priorityByID[result[i].ID]; ok {
			result[i].Priority = prio
			result[i].AccountGroups = []service.AccountGroup{{
				AccountID: result[i].ID,
				GroupID:   groupID,
				Priority:  prio,
			}}
			result[i].GroupIDs = []int64{groupID}
		}
	}
	return result, nil
}

func uniqueImageSizeAccountIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
