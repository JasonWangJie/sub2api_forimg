<template>
  <div class="overflow-x-auto">
    <table class="min-w-full text-left text-xs">
      <caption class="sr-only">{{ t('asyncImageTasks.attemptHistory.stageDetails') }}</caption>
      <thead class="bg-gray-50 text-[11px] text-gray-500 dark:bg-dark-900 dark:text-gray-400">
        <tr>
          <th v-for="column in columns" :key="column" scope="col" class="whitespace-nowrap px-4 py-2 font-medium">{{ t(`asyncImageTasks.detail.${column}`) }}</th>
        </tr>
      </thead>
      <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
        <tr v-for="(record, index) in records" :key="`${record.event_id || record.attempted_at}-${index}`" data-test="attempt-stage">
          <td class="whitespace-nowrap px-4 py-3 text-gray-500 dark:text-gray-400">{{ formatDateTime(record.attempted_at) || '-' }}</td>
          <td class="whitespace-nowrap px-4 py-3"><span :class="stageClass(record.status)">{{ stageLabel(record.status) }}</span></td>
          <td class="max-w-[220px] break-words px-4 py-3 text-gray-700 dark:text-gray-200">{{ record.account_name || `#${record.account_id}` }}<div class="mt-0.5 font-mono text-[11px] text-gray-400">#{{ record.account_id }}</div></td>
          <td class="whitespace-nowrap px-4 py-3 font-mono text-gray-600 dark:text-gray-300">{{ record.status_code ?? '-' }}</td>
          <td class="max-w-[180px] break-all px-4 py-3 font-mono text-gray-600 dark:text-gray-300">{{ record.provider_error_code || '-' }}</td>
          <td class="max-w-[220px] break-all px-4 py-3 font-mono text-gray-600 dark:text-gray-300">{{ record.upstream_request_id || '-' }}</td>
          <td class="min-w-[220px] max-w-[360px] break-words px-4 py-3 text-gray-600 dark:text-gray-300">
            <p>{{ record.error || '-' }}</p>
            <p v-if="record.failure_reason" class="mt-1 break-all font-mono text-[11px]">{{ record.failure_reason }}</p>
          </td>
          <td class="max-w-[220px] break-all px-4 py-3 font-mono text-[11px] text-gray-500 dark:text-gray-400">{{ record.event_id || '-' }}</td>
          <td class="max-w-[220px] break-all px-4 py-3 font-mono text-[11px] text-gray-500 dark:text-gray-400">{{ record.attempt_id || '-' }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { formatDateTime } from '@/utils/format'
import type { AsyncImageAccountAttempt } from './types'

defineProps<{ records: AsyncImageAccountAttempt[] }>()
const { t, te } = useI18n()
const columns = ['attemptedAt', 'attemptStatus', 'attemptAccount', 'httpStatus', 'providerErrorCode', 'upstreamRequestId', 'failureReason', 'eventId', 'attemptId']
function stageLabel(status: string) {
  const key = `asyncImageTasks.accountAttemptStatus.${status}`
  if (te(key)) return t(key)
  const statusKey = `asyncImageTasks.status.${status}`
  return te(statusKey) ? t(statusKey) : status || '-'
}
function stageClass(status: string) {
  const base = 'inline-flex rounded-md border px-2 py-1 text-xs font-medium'
  if (status === 'succeeded') return `${base} border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900/70 dark:bg-emerald-950/40 dark:text-emerald-300`
  if (status === 'failed') return `${base} border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-900/70 dark:bg-rose-950/40 dark:text-rose-300`
  if (status === 'selected' || status === 'invocation_started') return `${base} border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900/70 dark:bg-amber-950/40 dark:text-amber-300`
  return `${base} border-gray-200 bg-gray-50 text-gray-600 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-300`
}
</script>
