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
