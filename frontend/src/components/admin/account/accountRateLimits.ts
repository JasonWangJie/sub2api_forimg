import type { Account } from '@/types'

export interface AccountRateLimitEntry {
  kind: 'account' | 'model' | 'overload' | 'temporary' | 'quota'
  scope?: string
  until: string | null
}

const asRecord = (value: unknown): Record<string, unknown> | undefined =>
  value && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : undefined

/** Only local runtime restrictions cleared by the existing clear-rate-limit endpoint. */
export function getAccountRateLimitEntries(account: Account | null, now = Date.now()): AccountRateLimitEntry[] {
  if (!account) return []
  const entries: AccountRateLimitEntry[] = []
  const add = (kind: AccountRateLimitEntry['kind'], value: unknown, scope?: string) => {
    if (typeof value === 'string' && Date.parse(value) > now) {
      entries.push({ kind, scope, until: value })
    }
  }
  add('account', account.rate_limit_reset_at)
  add('overload', account.overload_until)
  add('temporary', account.temp_unschedulable_until)
  const extra = asRecord(account.extra)
  const models = asRecord(extra?.model_rate_limits)
  for (const [model, limit] of Object.entries(models ?? {})) {
    add('model', asRecord(limit)?.rate_limit_reset_at, model)
  }
  // Legacy Antigravity quota-scope markers have no standardized reset timestamp.
  if (Object.keys(asRecord(extra?.antigravity_quota_scopes) ?? {}).length > 0) {
    entries.push({ kind: 'quota', until: null })
  }
  return entries
}
