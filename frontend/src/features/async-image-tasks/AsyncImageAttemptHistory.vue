<template>
  <div v-if="attempts.length" class="mt-4 space-y-3" data-test="attempt-history">
    <p class="text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('asyncImageTasks.attemptHistory.note') }}</p>
    <ol v-if="history.groups.length" class="space-y-2" :aria-label="t('asyncImageTasks.attemptHistory.title')">
      <li v-for="(group, index) in history.groups" :key="group.id" data-test="call-attempt">
        <details class="group/attempt overflow-hidden rounded-md border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800">
          <summary class="cursor-pointer list-none px-4 py-3 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500 [&::-webkit-details-marker]:hidden">
            <span class="flex flex-wrap items-center gap-x-3 gap-y-2">
              <span class="flex w-full min-w-0 items-baseline gap-3 sm:w-auto sm:flex-1">
                <span class="shrink-0 font-mono text-xs text-gray-400">{{ t('asyncImageTasks.attemptHistory.number', { number: index + 1 }) }}</span>
                <span class="min-w-0 flex-1 break-words text-sm font-semibold text-gray-800 dark:text-gray-100">{{ accountLabel(group) }}</span>
              </span>
              <span :class="outcomeClass(group.outcome)" data-test="call-outcome">{{ t(`asyncImageTasks.attemptHistory.outcome.${group.outcome}`) }}</span>
              <span class="whitespace-nowrap font-mono text-xs tabular-nums text-gray-500 dark:text-gray-400" :title="t('asyncImageTasks.attemptHistory.duration')">{{ duration(group.durationMs) }}</span>
              <span class="inline-flex items-center gap-1 text-xs text-gray-500 dark:text-gray-400">
                {{ t('asyncImageTasks.attemptHistory.stages', { count: group.records.length }) }}
                <Icon name="chevronDown" size="sm" class="transition-transform group-open/attempt:rotate-180 motion-reduce:transition-none" aria-hidden="true" />
              </span>
            </span>
            <span v-if="group.terminal?.status === 'failed'" class="mt-2 block break-words text-xs leading-5 text-rose-700 dark:text-rose-300" data-test="call-failure">
              <span v-if="group.terminal.status_code" class="mr-2 font-mono">HTTP {{ group.terminal.status_code }}</span>
              <span v-if="group.terminal.provider_error_code" class="mr-2 font-mono">{{ group.terminal.provider_error_code }}</span>
              {{ group.terminal.error || group.terminal.failure_reason || t('asyncImageTasks.attemptHistory.noFailureReason') }}
            </span>
            <span v-if="group.postSuccessFailure" class="mt-2 block break-words text-xs leading-5 text-rose-700 dark:text-rose-300" data-test="post-success-failure">
              {{ t('asyncImageTasks.attemptHistory.postSuccessDiagnostic') }}:
              <span v-if="group.postSuccessFailure.status_code" class="mx-1 font-mono">HTTP {{ group.postSuccessFailure.status_code }}</span>
              <span v-if="group.postSuccessFailure.provider_error_code" class="mr-2 font-mono">{{ group.postSuccessFailure.provider_error_code }}</span>
              {{ group.postSuccessFailure.error || group.postSuccessFailure.failure_reason || t('asyncImageTasks.attemptHistory.noFailureReason') }}
            </span>
            <span v-if="nextAttempt(group, index)" class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400" data-test="call-next">{{ nextAttempt(group, index) }}</span>
          </summary>
          <div class="border-t border-gray-200 dark:border-dark-700">
            <p class="break-all px-4 py-2 font-mono text-[11px] text-gray-400">{{ t('asyncImageTasks.attemptHistory.attemptId') }}: {{ group.id }}</p>
            <AsyncImageAttemptRecords :records="group.records" />
          </div>
        </details>
      </li>
    </ol>
    <details v-if="history.ungrouped.length" class="rounded-md border border-gray-200 bg-gray-50/70 dark:border-dark-700 dark:bg-dark-900/60" data-test="ungrouped-attempts">
      <summary class="cursor-pointer px-4 py-3 text-sm font-medium text-gray-700 outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-gray-200">{{ t('asyncImageTasks.detail.accountAttemptHistory', { count: history.ungrouped.length }) }}</summary>
      <p class="border-t border-gray-200 px-4 py-3 text-xs leading-5 text-gray-500 dark:border-dark-700 dark:text-gray-400">{{ t('asyncImageTasks.attemptHistory.ungroupedNote') }}</p>
      <AsyncImageAttemptRecords :records="history.ungrouped" />
    </details>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import AsyncImageAttemptRecords from './AsyncImageAttemptRecords.vue'
import { buildAttemptHistory, type AttemptOutcome, type ImageCallAttempt } from './attemptHistory'
import type { AsyncImageAccountAttempt } from './types'

const props = defineProps<{ attempts: AsyncImageAccountAttempt[] }>()
const { t } = useI18n()
const history = computed(() => buildAttemptHistory(props.attempts))
const accountLabel = (group: ImageCallAttempt) => group.accountName ? `${group.accountName} · #${group.accountId}` : `#${group.accountId}`
const duration = (ms: number | null) => ms === null ? '—' : ms < 1000 ? `${Math.round(ms)} ms` : `${(ms / 1000).toFixed(1)} s`
function nextAttempt(group: ImageCallAttempt, index: number) {
  const next = history.value.groups[index + 1]
  if (!next || group.terminal?.status !== 'failed') return ''
  const key = next.accountId === group.accountId ? 'sameAccountRetry' : 'switchedAccount'
  return t(`asyncImageTasks.attemptHistory.${key}`, { account: accountLabel(next) })
}
function outcomeClass(outcome: AttemptOutcome) {
  const base = 'inline-flex shrink-0 rounded-md border px-2 py-1 text-xs font-medium'
  if (outcome === 'upstream_success') return `${base} border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900/70 dark:bg-emerald-950/40 dark:text-emerald-300`
  if (outcome === 'failed' || outcome === 'preflight_failed') return `${base} border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-900/70 dark:bg-rose-950/40 dark:text-rose-300`
  return `${base} border-gray-200 bg-gray-50 text-gray-600 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-300`
}
</script>
