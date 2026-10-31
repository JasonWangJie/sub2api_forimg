import type { AsyncImageAccountAttempt } from './types'

export type AttemptOutcome = 'upstream_success' | 'failed' | 'preflight_failed' | 'missing_end' | 'selected' | 'missing_start'

export interface ImageCallAttempt {
  id: string
  accountId: number
  accountName?: string | null
  records: AsyncImageAccountAttempt[]
  outcome: AttemptOutcome
  terminal?: AsyncImageAccountAttempt
  postSuccessFailure?: AsyncImageAccountAttempt
  durationMs: number | null
}

// Only an explicit shared identifier establishes that stages belong to one call.
// Old records and conflicting identifiers remain available as individual audits.
export function buildAttemptHistory(records: AsyncImageAccountAttempt[]) {
  const accountsById = new Map<string, Set<number>>()
  const startsById = new Map<string, number>()
  for (const record of records) {
    const id = record.attempt_id?.trim()
    if (!id) continue
    const accounts = accountsById.get(id) || new Set<number>()
    accounts.add(record.account_id)
    accountsById.set(id, accounts)
    if (record.status === 'invocation_started') startsById.set(id, (startsById.get(id) || 0) + 1)
  }

  const groups = new Map<string, ImageCallAttempt>()
  const ungrouped: AsyncImageAccountAttempt[] = []
  for (const record of records) {
    const id = record.attempt_id?.trim()
    if (!id || accountsById.get(id)?.size !== 1 || (startsById.get(id) || 0) > 1) {
      ungrouped.push(record)
      continue
    }
    let group = groups.get(id)
    if (!group) {
      group = { id, accountId: record.account_id, accountName: record.account_name, records: [], outcome: 'selected', durationMs: null }
      groups.set(id, group)
    }
    group.records.push(record)
    if (record.account_name) group.accountName = record.account_name
  }

  for (const group of groups.values()) {
    const started = group.records.find((record) => record.status === 'invocation_started')
    const successIndex = group.records.findIndex((record) => record.status === 'succeeded')
    // Fetching a successful upstream result may fail later. This is still one
    // upstream success: retain that boundary and show later diagnostics separately.
    group.terminal = successIndex >= 0 ? group.records[successIndex] : [...group.records].reverse().find((record) => record.status === 'failed')
    group.postSuccessFailure = successIndex < 0 ? undefined : group.records.slice(successIndex + 1).reverse().find((record) => record.status === 'failed')
    if (started) {
      group.outcome = !group.terminal ? 'missing_end' : group.terminal.status === 'succeeded' ? 'upstream_success' : 'failed'
    } else {
      group.outcome = !group.terminal ? 'selected' : group.terminal.status === 'failed' ? 'preflight_failed' : 'missing_start'
    }
    if (started && group.terminal) {
      const start = Date.parse(started.attempted_at)
      const end = Date.parse(group.terminal.attempted_at)
      if (Number.isFinite(start) && Number.isFinite(end) && end >= start) group.durationMs = end - start
    }
  }
  return { groups: [...groups.values()], ungrouped }
}

export function countGenerationRequests(records: AsyncImageAccountAttempt[], taskStatus: string): number | null {
  const { groups, ungrouped } = buildAttemptHistory(records)
  const calls = records.filter((record) => record.status === 'invocation_started').length
  if (groups.some((group) => group.outcome === 'missing_start')) return null
  // Mixed old/new histories may contain older calls without start auditing.
  if (ungrouped.length && !ungrouped.some((record) => record.status === 'invocation_started')) return null
  if (calls > 0) return calls
  if (records.length > 0 && ungrouped.length === 0) return 0
  // Missing start markers in old data cannot establish how many sends occurred.
  return taskStatus === 'queued' && records.length === 0 ? 0 : null
}
