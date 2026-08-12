<template>
  <div
    class="usage-stats-cards grid grid-cols-2 gap-4 lg:grid-cols-4"
    :class="{ 'usage-stats-cards--glow': glow }"
  >
    <div class="card usage-stat-card p-4 flex items-center gap-3">
      <div class="usage-stat-card__icon usage-stat-card__icon--sky">
        <Icon name="document" size="md" />
      </div>
      <div class="min-w-0">
        <p class="usage-stat-card__label">{{ t('usage.totalRequests') }}</p>
        <p class="usage-stat-card__value">{{ stats?.total_requests?.toLocaleString() || '0' }}</p>
        <p class="usage-stat-card__hint">{{ t('usage.inSelectedRange') }}</p>
      </div>
    </div>
    <div class="card usage-stat-card p-4 flex items-center gap-3">
      <div class="usage-stat-card__icon usage-stat-card__icon--amber">
        <svg class="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="m21 7.5-9-5.25L3 7.5m18 0-9 5.25m9-5.25v9l-9 5.25M3 7.5l9 5.25M3 7.5v9l9 5.25m0-9v9" />
        </svg>
      </div>
      <div class="min-w-0">
        <p class="usage-stat-card__label">{{ t('usage.totalTokens') }}</p>
        <p class="usage-stat-card__value">{{ formatTokens(stats?.total_tokens || 0) }}</p>
        <p class="usage-stat-card__hint flex flex-wrap items-center gap-x-1">
          <span>{{ t('usage.in') }}: {{ formatTokens(stats?.total_input_tokens || 0) }}</span>
          <span>/</span>
          <span>{{ t('usage.out') }}: {{ formatTokens(stats?.total_output_tokens || 0) }}</span>
          <span>/</span>
          <span class="group relative inline-flex cursor-help items-center gap-0.5" tabindex="0">
            <span>{{ cacheLabel() }}: {{ formatTokens(stats?.total_cache_tokens || 0) }}</span>
            <svg class="h-3.5 w-3.5 opacity-60" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <span
              class="pointer-events-none absolute left-1/2 top-full z-30 mt-2 w-56 -translate-x-1/2 rounded-lg border border-gray-200 bg-white p-3 text-left text-xs text-gray-700 opacity-0 shadow-lg transition-opacity duration-150 group-hover:opacity-100 group-focus:opacity-100 dark:border-dark-600 dark:bg-dark-800 dark:text-dark-200"
            >
              <span class="mb-2 block font-medium text-gray-900 dark:text-white">
                {{ cacheDetailLabel() }}
              </span>
              <span class="flex items-center justify-between gap-3">
                <span>{{ t('usage.cacheCreationTokensLabel') }}</span>
                <span class="tabular-nums">
                  {{ formatTokens(stats?.total_cache_creation_tokens || 0) }}
                </span>
              </span>
              <span class="mt-1 flex items-center justify-between gap-3">
                <span>{{ t('usage.cacheReadTokensLabel') }}</span>
                <span class="tabular-nums">
                  {{ formatTokens(stats?.total_cache_read_tokens || 0) }}
                </span>
              </span>
            </span>
          </span>
        </p>
      </div>
    </div>
    <div class="card usage-stat-card p-4 flex items-center gap-3">
      <div class="usage-stat-card__icon usage-stat-card__icon--mint">
        <Icon name="dollar" size="md" />
      </div>
      <div class="min-w-0 flex-1">
        <p class="usage-stat-card__label">{{ t('usage.totalCost') }}</p>
        <p class="usage-stat-card__value usage-stat-card__value--mint">
          ${{ (stats?.total_actual_cost || 0).toFixed(4) }}
        </p>
        <p class="usage-stat-card__hint">
          <template v-if="showAccountCost && totalAccountCost != null">
            <span class="text-orange-500 dark:text-orange-300">{{ t('usage.accountCost') }} ${{ totalAccountCost.toFixed(4) }}</span>
            <span> · </span>
          </template>
          <span>
            {{ t('usage.standardCost') }}
            <span :class="{ 'line-through': strikeStandardCost }">${{ (stats?.total_cost || 0).toFixed(4) }}</span>
          </span>
        </p>
      </div>
    </div>
    <div class="card usage-stat-card p-4 flex items-center gap-3">
      <div class="usage-stat-card__icon usage-stat-card__icon--teal">
        <Icon name="clock" size="md" />
      </div>
      <div class="min-w-0">
        <p class="usage-stat-card__label">{{ t('usage.avgDuration') }}</p>
        <p class="usage-stat-card__value">{{ formatDuration(stats?.average_duration_ms || 0) }}</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdminUsageStatsResponse } from '@/api/admin/usage'
import type { UsageStatsResponse } from '@/types'
import Icon from '@/components/icons/Icon.vue'

const props = withDefaults(defineProps<{
  stats: (AdminUsageStatsResponse | UsageStatsResponse) | null
  showAccountCost?: boolean
  strikeStandardCost?: boolean
  /** Enable dashboard-style border glow animation (user usage page). */
  glow?: boolean
}>(), {
  showAccountCost: true,
  strikeStandardCost: false,
  glow: false,
})

const { t } = useI18n()

const totalAccountCost = computed(() => {
  const stats = props.stats as (AdminUsageStatsResponse & { total_account_cost?: number }) | null
  return stats?.total_account_cost ?? null
})
const showAccountCost = computed(() => props.showAccountCost)
const strikeStandardCost = computed(() => props.strikeStandardCost)

const formatDuration = (ms: number) =>
  ms < 1000 ? `${ms.toFixed(0)}ms` : `${(ms / 1000).toFixed(2)}s`

const formatTokens = (value: number) => {
  if (value >= 1e9) return (value / 1e9).toFixed(2) + 'B'
  if (value >= 1e6) return (value / 1e6).toFixed(2) + 'M'
  if (value >= 1e3) return (value / 1e3).toFixed(2) + 'K'
  return value.toLocaleString()
}

const cacheLabel = () => t('usage.cacheTotal')
const cacheDetailLabel = () => t('usage.cacheBreakdown')
</script>

<style scoped>
.usage-stat-card__icon {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border-radius: 0.7rem;
  padding: 0.5rem;
}

.usage-stat-card__icon--sky {
  background: rgba(224, 242, 254, 0.95);
  color: #0284c7;
}

.usage-stat-card__icon--amber {
  background: rgba(254, 243, 199, 0.95);
  color: #d97706;
}

.usage-stat-card__icon--mint {
  background: rgba(209, 250, 229, 0.95);
  color: #059669;
}

.usage-stat-card__icon--teal {
  background: rgba(204, 251, 241, 0.95);
  color: #0d9488;
}

.usage-stat-card__label {
  margin: 0;
  font-size: 0.75rem;
  font-weight: 600;
  color: #64748b;
}

.usage-stat-card__value {
  margin: 0.1rem 0 0;
  font-size: 1.25rem;
  font-weight: 750;
  letter-spacing: -0.02em;
  color: #0f172a;
  tabular-nums: true;
  font-variant-numeric: tabular-nums;
}

.usage-stat-card__value--mint {
  color: #059669;
}

.usage-stat-card__hint {
  margin: 0.15rem 0 0;
  font-size: 0.75rem;
  color: #94a3b8;
}
</style>

<style>
/* Dark-mode overrides kept unscoped: Vue scoped compiler drops :global(.dark) in production. */
.dark .usage-stat-card__icon--sky {
  background: rgba(14, 165, 233, 0.16);
  color: #7dd3fc;
}

.dark .usage-stat-card__icon--amber {
  background: rgba(245, 158, 11, 0.16);
  color: #fbbf24;
}

.dark .usage-stat-card__icon--mint {
  background: rgba(16, 185, 129, 0.16);
  color: #34d399;
}

.dark .usage-stat-card__icon--teal {
  background: rgba(20, 184, 166, 0.16);
  color: #2dd4bf;
}

.dark .usage-stat-card__label {
  color: #94a3b8;
}

.dark .usage-stat-card__value {
  color: #f8fafc;
}

.dark .usage-stat-card__value--mint {
  color: #34d399;
}

.dark .usage-stat-card__hint {
  color: #64748b;
}
</style>
