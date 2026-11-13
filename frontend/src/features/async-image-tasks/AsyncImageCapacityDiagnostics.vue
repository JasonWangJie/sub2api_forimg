<template>
  <section v-if="admin && snapshots.length" data-testid="capacity-diagnostics" class="rounded-lg border border-amber-200 bg-amber-50/40 p-4 dark:border-amber-900/60 dark:bg-amber-950/10">
    <h4 class="text-sm font-semibold text-gray-900 dark:text-gray-100">{{ t('asyncImageTasks.capacity.title') }}</h4>
    <p class="mt-1 text-xs leading-5 text-gray-600 dark:text-gray-400">{{ t('asyncImageTasks.capacity.note') }}</p>
    <details v-for="(snapshot, index) in snapshots" :key="`${snapshot.observed_at}-${index}`" :open="index === 0" class="mt-3 rounded-md border border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-800">
      <summary class="cursor-pointer break-words text-sm font-medium text-gray-800 dark:text-gray-100">
        {{ formatDateTime(snapshot.observed_at) }} · {{ snapshot.model }} · {{ snapshot.size_tier || '—' }} · {{ t('asyncImageTasks.capacity.group', { id: snapshot.group_id }) }}
      </summary>
      <div v-for="(observation, stage) in snapshot.observations" :key="stage" class="mt-4 border-t border-gray-100 pt-3 dark:border-dark-700">
        <div class="flex flex-wrap items-center justify-between gap-2 text-sm">
          <h5 class="font-medium text-gray-900 dark:text-gray-100">{{ label('sources', observation.source) }}</h5>
          <span class="tabular-nums text-gray-500 dark:text-gray-400">{{ observation.candidate_count === 0 && observation.source !== 'pool_snapshot' ? t('asyncImageTasks.capacity.candidateCountUnknown') : t('asyncImageTasks.capacity.candidates', { count: observation.candidate_count }) }}</span>
        </div>
        <p v-if="observation.observed_at" class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ formatDateTime(observation.observed_at) }}</p>
        <p v-if="observation.source === 'pool_snapshot'" class="mt-1 text-xs leading-5 text-amber-700 dark:text-amber-300">{{ t('asyncImageTasks.capacity.snapshotNote') }}</p>
        <p v-if="observation.pool_mode" class="mt-1 break-words text-xs text-gray-500 dark:text-gray-400">{{ t('asyncImageTasks.capacity.pool') }}: {{ label('pools', observation.pool_mode) }}<span v-if="observation.pool_model"> · {{ observation.pool_model }}</span><span v-if="observation.pool_size_tier"> · {{ observation.pool_size_tier }}</span></p>
        <ul class="mt-2 space-y-2">
          <li v-for="(reason, reasonIndex) in observation.reasons" :key="reasonIndex" class="rounded-md bg-gray-50 p-2 text-sm dark:bg-dark-900/60">
            <div class="flex flex-wrap items-start justify-between gap-2"><span class="text-gray-800 dark:text-gray-200">{{ label('reasons', reason.code) }}</span><span class="tabular-nums text-gray-500">{{ reason.count }}</span></div>
            <p v-if="reason.account_ids?.length" class="mt-1 break-words text-xs text-gray-500 dark:text-gray-400">{{ t('asyncImageTasks.capacity.accounts') }}: {{ reason.account_ids.map(id => `#${id}`).join(', ') }}</p>
            <p v-if="reason.earliest_recovery_at" class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('asyncImageTasks.capacity.recovery') }}: {{ formatDateTime(reason.earliest_recovery_at) }}</p>
          </li>
        </ul>
        <p v-if="observation.candidates_truncated" class="mt-2 text-xs text-amber-700 dark:text-amber-300">{{ t('asyncImageTasks.capacity.truncated') }}</p>
      </div>
    </details>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { formatDateTime } from '@/utils/format'
import type { AsyncImageTaskEvent, AsyncImageCapacityDiagnostics } from './types'
const props = defineProps<{ admin: boolean; events: AsyncImageTaskEvent[] }>()
const { t, te } = useI18n()
const snapshots = computed(() => props.admin ? props.events.map(event => event.capacity_diagnostics).filter((entry): entry is AsyncImageCapacityDiagnostics => Boolean(entry)).sort((a, b) => Date.parse(b.observed_at) - Date.parse(a.observed_at)) : [])
const label = (category: string, code: string) => {
  const key = `asyncImageTasks.capacity.${category}.${code}`
  return t(te(key) ? key : `asyncImageTasks.capacity.${category}.unknown`)
}
</script>
