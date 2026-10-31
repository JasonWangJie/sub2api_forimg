import { describe, expect, it } from 'vitest'
import { buildAttemptHistory, countGenerationRequests } from '../attemptHistory'
import type { AsyncImageAccountAttempt } from '../types'

function stage(status: string, second: number, attempt_id?: string, account_id = 2): AsyncImageAccountAttempt {
  return { account_id, attempt_id, status, attempted_at: `2026-10-31T10:00:${String(second).padStart(2, '0')}Z` }
}

describe('explicit image call attempts', () => {
  it('groups one call while preserving distinct retries on the same account and account switches', () => {
    const records = [
      stage('selected', 0, 'call-a'), stage('invocation_started', 1, 'call-a'), stage('failed', 3, 'call-a'),
      stage('invocation_started', 4, 'call-b'), stage('failed', 6, 'call-b'),
      stage('selected', 7, 'call-c', 3), stage('invocation_started', 8, 'call-c', 3), stage('succeeded', 10, 'call-c', 3),
    ]
    const history = buildAttemptHistory(records)
    expect(history.groups.map(group => [group.id, group.accountId, group.outcome, group.durationMs])).toEqual([
      ['call-a', 2, 'failed', 2000], ['call-b', 2, 'failed', 2000], ['call-c', 3, 'upstream_success', 2000],
    ])
    expect(history.groups.flatMap(group => group.records)).toEqual(records)
    expect(countGenerationRequests(records, 'succeeded')).toBe(3)
  })

  it('keeps upstream success and duration when later result processing fails', () => {
    const records = [stage('selected', 0, 'a'), stage('invocation_started', 1, 'a'), stage('succeeded', 4, 'a'), stage('failed', 20, 'a')]
    const group = buildAttemptHistory(records).groups[0]
    expect(group.outcome).toBe('upstream_success')
    expect(group.durationMs).toBe(3000)
    expect(group.postSuccessFailure).toBe(records[3])
    expect(group.records).toHaveLength(4)
    expect(countGenerationRequests(records, 'failed')).toBe(1)
  })

  it('does not count selection-only failures as sends or imply unfinished histories succeeded', () => {
    const records = [stage('selected', 0, 'preflight'), stage('failed', 1, 'preflight'), stage('selected', 2, 'selection'), stage('invocation_started', 4, 'missing')]
    const history = buildAttemptHistory(records)
    expect(history.groups.map(group => group.outcome)).toEqual(['preflight_failed', 'selected', 'missing_end'])
    expect(history.groups.every(group => group.durationMs === null)).toBe(true)
    expect(countGenerationRequests(records, 'failed')).toBe(1)
    expect(countGenerationRequests(records.slice(0, 2), 'failed')).toBe(0)
  })

  it('leaves old records and conflicting IDs ungrouped without inferring triples or accounts', () => {
    const old = [stage('selected', 0), stage('invocation_started', 1), stage('succeeded', 2)]
    const conflict = [stage('selected', 3, 'conflict'), stage('succeeded', 4, 'conflict', 3)]
    const history = buildAttemptHistory([...old, ...conflict])
    expect(history.groups).toEqual([])
    expect(history.ungrouped).toEqual([...old, ...conflict])
    expect(countGenerationRequests(old, 'succeeded')).toBe(1)
    expect(countGenerationRequests([old[0], old[2]], 'succeeded')).toBeNull()
  })

  it('keeps incomplete old/new totals unknown and empty queued sends at zero', () => {
    const mixed = [stage('succeeded', 0), stage('invocation_started', 1, 'new'), stage('succeeded', 2, 'new')]
    expect(countGenerationRequests(mixed, 'succeeded')).toBeNull()
    expect(countGenerationRequests([], 'queued')).toBe(0)
    expect(countGenerationRequests([], 'succeeded')).toBeNull()
    expect(countGenerationRequests([stage('succeeded', 0, 'no-start')], 'succeeded')).toBeNull()
    expect(buildAttemptHistory([stage('succeeded', 0, 'no-start')]).groups[0].outcome).toBe('missing_start')
  })

  it('does not merge multiple send starts sharing an invalid call ID or undercount them', () => {
    const records = [stage('selected', 0, 'conflict'), stage('invocation_started', 1, 'conflict'), stage('failed', 2, 'conflict'), stage('invocation_started', 3, 'conflict'), stage('succeeded', 4, 'conflict')]
    expect(buildAttemptHistory(records).groups).toEqual([])
    expect(buildAttemptHistory(records).ungrouped).toEqual(records)
    expect(countGenerationRequests(records, 'succeeded')).toBe(2)
  })

  it('does not fabricate duration from invalid or reversed timestamps', () => {
    expect(buildAttemptHistory([stage('invocation_started', 5, 'a'), stage('failed', 1, 'a')]).groups[0].durationMs).toBeNull()
    expect(buildAttemptHistory([{ ...stage('invocation_started', 0, 'a'), attempted_at: 'invalid' }, stage('failed', 1, 'a')]).groups[0].durationMs).toBeNull()
  })
})
