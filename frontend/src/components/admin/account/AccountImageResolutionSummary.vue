<template>
  <button
    v-if="supported && (!account.image_resolution_guard || activeItems.length)"
    type="button"
    class="group flex w-full min-w-[150px] max-w-[260px] flex-col gap-1 rounded-lg border px-2 py-1.5 text-left text-xs transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500/40"
    :class="isBlocking ? 'border-amber-200 bg-amber-50/70 text-amber-900 hover:bg-amber-100 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-200 dark:hover:bg-amber-950/60' : 'border-gray-200 bg-gray-50 text-gray-600 hover:bg-gray-100 dark:border-dark-600 dark:bg-dark-900/40 dark:text-gray-400 dark:hover:bg-dark-700'"
    :aria-label="t('admin.accounts.imageResolutionGuard.viewAccountDetails', { name: account.name })"
    data-testid="resolution-summary"
    @click.stop="emit('open', account)"
  >
    <span v-if="!account.image_resolution_guard" class="font-medium" data-testid="resolution-summary-unknown">{{ t('admin.accounts.imageResolutionGuard.stateUnavailable') }}</span>
    <template v-else>
      <span class="flex flex-wrap items-center gap-x-1.5 gap-y-0.5 font-medium">
        <span>{{ t(isBlocking ? 'admin.accounts.imageResolutionGuard.rowCooling' : 'admin.accounts.imageResolutionGuard.rowRecords') }}</span>
        <span v-if="!account.image_resolution_guard.enabled" class="rounded bg-gray-200/70 px-1 py-0.5 text-[10px] font-normal dark:bg-dark-600" data-testid="resolution-summary-disabled">{{ t('admin.accounts.imageResolutionGuard.protectionDisabled') }}</span>
      </span>
      <span v-for="item in activeItems.slice(0, 3)" :key="item.cycle_id" class="block w-full border-t pt-1" :class="isBlocking ? 'border-amber-200/70 dark:border-amber-900' : 'border-gray-200 dark:border-dark-600'" data-testid="resolution-summary-item">
        <span class="flex items-baseline gap-1.5">
          <span class="min-w-0 flex-1 break-all font-medium">{{ item.model }}</span>
          <span class="shrink-0 font-semibold">{{ item.size_tier }}</span>
        </span>
        <span class="mt-0.5 block text-[10px] tabular-nums opacity-80">{{ t('admin.accounts.imageResolutionGuard.rowUntil', { time: formatDateTime(item.cooldown_until) }) }}</span>
      </span>
      <span v-if="activeItems.length > 3" class="text-[10px] underline decoration-dotted underline-offset-2">{{ t('admin.accounts.imageResolutionGuard.moreCooldowns', { count: activeItems.length - 3 }) }}</span>
      <span class="mt-0.5 text-[10px] opacity-80 underline decoration-dotted underline-offset-2 group-hover:opacity-100">{{ t('admin.accounts.imageResolutionGuard.viewAndRestore') }}</span>
    </template>
  </button>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account } from '@/types'
import { formatDateTime } from '@/utils/format'

const props = defineProps<{ account: Account; now: number }>()
const emit = defineEmits<{ (event: 'open', account: Account): void }>()
const { t } = useI18n()
const supported = computed(() => ['openai', 'gemini', 'antigravity'].includes(props.account.platform))
const activeItems = computed(() => (props.account.image_resolution_guard?.cooldowns ?? [])
  .filter(item => Number.isFinite(Date.parse(item.cooldown_until)) && Date.parse(item.cooldown_until) > props.now)
  .slice().sort((a, b) => Date.parse(a.cooldown_until) - Date.parse(b.cooldown_until) || a.model.localeCompare(b.model)))
const isBlocking = computed(() => props.account.image_resolution_guard?.enabled === true && activeItems.value.length > 0)
</script>
