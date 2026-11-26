import { apiClient } from '../client'
import type { PaginatedResponse } from '@/types'

export const retryStages = ['prepare', 'submit', 'download', 'storage', 'billing'] as const
export type RetryStage = typeof retryStages[number]
export const retryActions = [
  'retry_prepare', 'same_account', 'other_account', 'local_same_account', 'local_other_account',
  'resume_download', 'resume_storage', 'resume_billing', 'stop', 'manual_review',
] as const
export type RetryAction = typeof retryActions[number]
export type RetryEndAction = 'stop' | 'manual_review'

export interface RetryScope {
  platforms: string[]
  models: string[]
  group_ids: number[]
  source_account_ids: number[]
  stages: RetryStage[]
  categories: string[]
}

export interface RetryMatch {
  http_statuses: number[]
  error_codes: string[]
  contains_any: string[]
  contains_all: string[]
  excludes: string[]
  regex: string
}

export interface RetryStep {
  id: string
  action: RetryAction
  max_attempts: number
  delay_seconds: number
  backoff: 'fixed' | 'exponential'
  max_delay_seconds: number
}

export interface RetryRule {
  id: string
  name: string
  enabled: boolean
  scope: RetryScope
  match: RetryMatch
  category: string
  allowed_account_ids: number[]
  excluded_account_ids: number[]
  max_attempts: number
  steps: RetryStep[]
  on_exhausted: RetryEndAction
}

export interface RetryConfig {
  revision: number
  pause_generation_retries: boolean
  rules: RetryRule[]
}

export interface RetryFact {
  task_id?: string
  failure_id?: string
  attempt_id?: string
  platform?: string
  model?: string
  stage?: RetryStage
  category?: string
  error_code?: string
  message?: string
  group_id?: number
  account_id?: number
  http_status?: number
  occurred_at?: string
  safe_to_retry?: boolean
  has_output?: boolean
  has_references?: boolean
  dispatched?: boolean
  unknown?: boolean
}

export interface RetryDecision {
  source: 'custom' | 'builtin' | 'none'
  rule_id?: string
  step_id?: string
  category?: string
  requested_action?: RetryAction
  action?: RetryAction
  block_reason?: string
  revision?: number
  delay_seconds?: number
  allowed_account_ids?: number[]
  excluded_account_ids?: number[]
}

export interface RetryEvent {
  id: number
  fact: RetryFact
  decision: RetryDecision
  fingerprint: string
  started_at?: string
  generation_started_at?: string
  target_account_id?: number
  final_status?: string
  created_at: string
}

export interface RetrySample {
  fingerprint: string
  latest: RetryEvent
  occurrences: number
  tasks: number
  executions: number
  succeeded_tasks: number
  last_seen_at: string
}

export interface RetryVersion {
  revision: number
  actor_id?: number
  created_at: string
  operation: string
  rollback_from?: number
  policy?: RetryConfig
}

export interface RetrySampleFilters {
  from?: string
  to?: string
  platform?: string
  model?: string
  stage?: string
  category?: string
  error_code?: string
  group_id?: number
  account_id?: number
  http_status?: number
  source?: string
}

export interface RetryPreview {
  decision: RetryDecision
  historical?: RetryDecision
  candidate_account_ids?: number[]
  eligibility: string
  eligibility_reason?: string
}

type PreviewInput = { draft: RetryConfig } & ({ fact: RetryFact } | { event_id: number })
type PageParams = { page: number; page_size: number }

const base = '/admin/async-image-retry-rules'

export const asyncImageRetryRulesAPI = {
  async getConfig(signal?: AbortSignal): Promise<RetryConfig> {
    const { data } = await apiClient.get<RetryConfig>(base, { signal })
    return data
  },
  async save(config: RetryConfig): Promise<RetryConfig> {
    const { data } = await apiClient.put<RetryConfig>(base, config)
    return data
  },
  async samples(params: RetrySampleFilters & PageParams, signal?: AbortSignal): Promise<PaginatedResponse<RetrySample>> {
    const { data } = await apiClient.get<PaginatedResponse<RetrySample>>(`${base}/samples`, { params, signal })
    return data
  },
  async sampleEvents(fingerprint: string, params: RetrySampleFilters & PageParams, signal?: AbortSignal): Promise<PaginatedResponse<RetryEvent>> {
    const url = `${base}/samples/${encodeURIComponent(fingerprint)}/events`
    const { data } = await apiClient.get<PaginatedResponse<RetryEvent>>(url, { params, signal })
    return data
  },
  async event(id: number, signal?: AbortSignal): Promise<RetryEvent> {
    const { data } = await apiClient.get<RetryEvent>(`${base}/events/${id}`, { signal })
    return data
  },
  async preview(input: PreviewInput, signal?: AbortSignal): Promise<RetryPreview> {
    const { data } = await apiClient.post<RetryPreview>(`${base}/preview`, input, { signal })
    return data
  },
  async versions(params: PageParams, signal?: AbortSignal): Promise<PaginatedResponse<RetryVersion>> {
    const { data } = await apiClient.get<PaginatedResponse<RetryVersion>>(`${base}/versions`, { params, signal })
    return data
  },
  async version(revision: number, signal?: AbortSignal): Promise<RetryVersion> {
    const { data } = await apiClient.get<RetryVersion>(`${base}/versions/${revision}`, { signal })
    return data
  },
  async rollback(revision: number, targetRevision: number): Promise<RetryConfig> {
    const { data } = await apiClient.post<RetryConfig>(`${base}/rollback`, {
      revision,
      target_revision: targetRevision,
    })
    return data
  },
}
