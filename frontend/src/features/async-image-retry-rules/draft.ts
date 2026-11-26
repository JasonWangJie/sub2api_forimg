import type { RetryAction, RetryConfig, RetryFact, RetryRule, RetryStage, RetryStep } from '@/api/admin/asyncImageRetryRules'

export const cloneDraft = <T>(value: T): T => JSON.parse(JSON.stringify(value)) as T

function newID() {
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  return Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')
}

export function newStep(): RetryStep {
  return {
    id: newID(), action: 'other_account', max_attempts: 1,
    delay_seconds: 0, backoff: 'fixed', max_delay_seconds: 0,
  }
}

export function newRule(): RetryRule {
  return {
    id: newID(), name: '', enabled: true,
    scope: { platforms: [], models: [], group_ids: [], source_account_ids: [], stages: [], categories: [] },
    match: { http_statuses: [], error_codes: [], contains_any: [], contains_all: [], excludes: [], regex: '' },
    category: '', allowed_account_ids: [], excluded_account_ids: [], max_attempts: 0,
    steps: [newStep()], on_exhausted: 'manual_review',
  }
}

export function duplicateRule(rule: RetryRule, name: string): RetryRule {
  const copy = cloneDraft(rule)
  copy.id = newID()
  copy.name = name
  for (const step of copy.steps) step.id = newID()
  return copy
}

export function matchingRules(rules: RetryRule[], search: string): RetryRule[] {
  const query = search.trim().toLowerCase()
  return rules.filter(rule => rule.name.toLowerCase().includes(query) || rule.id.toLowerCase().includes(query))
}

export function moveRule(config: RetryConfig, ruleID: string, delta: -1 | 1): void {
  const index = config.rules.findIndex(rule => rule.id === ruleID)
  const destination = index + delta
  if (index < 0 || destination < 0 || destination >= config.rules.length) return
  const [rule] = config.rules.splice(index, 1)
  config.rules.splice(destination, 0, rule)
}

export function buildRuleFromError(fact: RetryFact): RetryRule {
  const rule = newRule()
  const stageActions: Record<RetryStage, RetryAction> = {
    prepare: 'retry_prepare', submit: 'other_account', download: 'resume_download',
    storage: 'resume_storage', billing: 'resume_billing',
  }
  rule.scope.platforms = fact.platform ? [fact.platform] : []
  rule.scope.models = fact.model ? [fact.model] : []
  rule.scope.stages = fact.stage ? [fact.stage] : []
  rule.scope.categories = fact.category ? [fact.category] : []
  rule.scope.source_account_ids = fact.account_id ? [fact.account_id] : []
  rule.match.http_statuses = fact.http_status ? [fact.http_status] : []
  rule.match.error_codes = fact.error_code ? [fact.error_code] : []
  if (fact.stage) rule.steps[0].action = stageActions[fact.stage] || 'manual_review'
  if (!fact.error_code && !fact.http_status && !fact.category && fact.message) {
    rule.match.contains_any = [fact.message.slice(0, 160)]
  }
  return rule
}

export async function previewPage<T>(items: readonly T[], run: (item: T) => Promise<void>, signal: AbortSignal) {
  const queue = [...items]
  const take = async () => {
    while (queue.length && !signal.aborted) {
      const item = queue.shift()!
      await run(item)
    }
  }
  await Promise.all(Array.from({ length: Math.min(4, queue.length) }, take))
}
