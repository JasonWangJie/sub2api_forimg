package service

import "context"

type forcedSchedulableAccountsKey struct{}

// withForcedSchedulableAccounts forces listSchedulableAccounts to use a fixed candidate set.
// Used by image size-tier pools so independently bound accounts can be selected.
func withForcedSchedulableAccounts(ctx context.Context, accounts []Account) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	cloned := append([]Account(nil), accounts...)
	return context.WithValue(ctx, forcedSchedulableAccountsKey{}, cloned)
}

func forcedSchedulableAccountsFromContext(ctx context.Context) ([]Account, bool) {
	if ctx == nil {
		return nil, false
	}
	accounts, ok := ctx.Value(forcedSchedulableAccountsKey{}).([]Account)
	if !ok || accounts == nil {
		return nil, false
	}
	return accounts, true
}

func forcedSchedulableAccountContains(ctx context.Context, accountID int64) bool {
	account, forced := forcedSchedulableAccountFromContext(ctx, accountID)
	return forced && account != nil
}

// forcedSchedulableAccountFromContext returns a candidate from a constrained
// pool. The second result reports whether a constrained pool is active even
// when the requested account is outside it.
func forcedSchedulableAccountFromContext(ctx context.Context, accountID int64) (*Account, bool) {
	accounts, forced := forcedSchedulableAccountsFromContext(ctx)
	if !forced {
		return nil, false
	}
	for i := range accounts {
		if accounts[i].ID == accountID {
			account := accounts[i]
			return &account, true
		}
	}
	return nil, true
}
