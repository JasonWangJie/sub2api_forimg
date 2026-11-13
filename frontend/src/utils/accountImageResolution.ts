import type { Account } from '@/types'

// Keep runtime-only quality changes visible even when accounts.updated_at did
// not change. Sorting avoids replacing rows merely because SQL order changed.
export function accountImageResolutionRefreshKey(account: Account): string {
  const state = account.image_resolution_guard
  if (!state) return 'unknown'
  const cooldowns = (state.cooldowns ?? []).map(item => [item.cycle_id, item.model, item.size_tier, item.cooldown_until].join('\u0000')).sort()
  return JSON.stringify([state.enabled, cooldowns])
}
