<template>
  <section v-if="stats" class="mt-3 rounded-lg border border-gray-200 bg-white px-4 py-3 dark:border-dark-700 dark:bg-dark-800" :aria-label="t('asyncImageTasks.reliability.title')" data-test="reliability-summary">
    <div class="mb-3 flex flex-wrap items-baseline justify-between gap-2">
      <h3 class="text-sm font-semibold text-gray-800 dark:text-gray-100">{{ t('asyncImageTasks.reliability.title') }}</h3>
      <p class="text-xs text-gray-500 dark:text-dark-400">{{ t('asyncImageTasks.reliability.scope') }} · {{ t('asyncImageTasks.reliability.cost') }} {{ formatCurrency(stats.actual_cost) }}</p>
    </div>
    <dl class="grid grid-cols-2 gap-x-6 gap-y-3 md:grid-cols-4 xl:grid-cols-6">
      <div v-for="metric in metrics" :key="metric.key">
        <dt class="text-xs text-gray-500 dark:text-dark-400">{{ t(`asyncImageTasks.reliability.${metric.key}`) }}</dt>
        <dd class="mt-1 text-sm font-semibold tabular-nums text-gray-900 dark:text-gray-100">{{ metric.value }}</dd>
      </div>
    </dl>
    <p class="mt-3 border-t border-gray-100 pt-2 text-xs leading-relaxed text-gray-500 dark:border-dark-700 dark:text-dark-400">{{ t('asyncImageTasks.reliability.note') }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatCurrency } from '@/utils/format'
import type { AsyncImageReliabilityStats } from './types'

const props = defineProps<{ stats?: AsyncImageReliabilityStats | null }>()
const { t } = useI18n()
const duration = (ms: number | null | undefined) => ms == null || !Number.isFinite(ms) ? '—' : `${(ms / 1000).toFixed(1)} s`
const ratio = (success: number, total: number) => total > 0 ? `${success} / ${total} · ${(success / total * 100).toFixed(1)}%` : '—'
const metrics = computed(() => {
  const s = props.stats
  if (!s) return []
  return [
    { key: 'first', value: ratio(s.first_attempt_succeeded, s.instrumented_tasks) },
    { key: 'switch', value: ratio(s.multi_account_succeeded, s.multi_account_tasks) },
    { key: 'local', value: ratio(s.local_fallback_succeeded, s.local_fallback_tasks) },
    { key: 'result', value: ratio(s.result_fetch_recovered_tasks, s.result_fetch_retried_tasks) },
    { key: 'unknown', value: `${s.unknown} / ${s.total} · ${s.total ? (s.unknown / s.total * 100).toFixed(2) : '0.00'}%` },
    { key: 'calls', value: `${s.http_invocations} / ${s.account_selections}` },
    { key: 'p95', value: `${duration(s.p95_duration_ms)} / ${duration(s.p99_duration_ms)}` },
    { key: 'queue', value: duration(s.p95_queue_ms) },
    { key: 'generation', value: duration(s.p95_generation_ms) },
    { key: 'postprocess', value: duration(s.p95_postprocess_ms) },
    { key: 'oldest', value: duration(s.oldest_queued_age_ms) },
    { key: 'history', value: `${s.unknown_events} / ${s.admin_terminated} / ${s.expired}` },
  ]
})
</script>
