import { describe, expect, it } from 'vitest'
import type { Account, AccountImageResolutionGuard } from '@/types'
import { accountImageResolutionRefreshKey } from '../accountImageResolution'

const state: AccountImageResolutionGuard = { enabled: true, cooldowns: [{ account_id: 12, model: 'gpt-image-2', size_tier: '4K', cycle_id: 'cycle-1', cooldown_until: '2099-01-01T00:00:00Z', reason: 'image_resolution_downgrade' }] }
const key = (guard?: AccountImageResolutionGuard) => accountImageResolutionRefreshKey({ image_resolution_guard: guard } as Account)

describe('account image resolution runtime refresh', () => {
  it('refreshes rows for newly cooled models, expired/cleared cycles and protection switch changes', () => {
    expect(key(state)).not.toBe(key({ enabled: true, cooldowns: [] }))
    expect(key(state)).not.toBe(key({ ...state, enabled: false }))
    expect(key(state)).not.toBe(key({ ...state, cooldowns: [{ ...state.cooldowns[0], cycle_id: 'new-cycle' }] }))
    expect(key(state)).not.toBe(key({ ...state, cooldowns: [{ ...state.cooldowns[0], model: 'gpt-image-2.5-flare' }] }))
    expect(key(state)).not.toBe(key({ ...state, cooldowns: [{ ...state.cooldowns[0], size_tier: '2K' }] }))
    expect(key(state)).not.toBe(key({ ...state, cooldowns: [{ ...state.cooldowns[0], cooldown_until: '2099-01-02T00:00:00Z' }] }))
  })

  it('distinguishes a failed or missing state read from confirmed empty state', () => {
    expect(key(undefined)).not.toBe(key({ enabled: true, cooldowns: [] }))
  })

  it('does not churn account rows merely because cooldown SQL ordering changed', () => {
    const two = [state.cooldowns[0], { ...state.cooldowns[0], model: 'another-model', cycle_id: 'cycle-2' }]
    expect(key({ enabled: true, cooldowns: two })).toBe(key({ enabled: true, cooldowns: two.slice().reverse() }))
  })
})
