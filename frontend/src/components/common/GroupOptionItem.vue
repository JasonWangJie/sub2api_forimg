<template>
  <div
    :class="[
      'group-option-item flex min-w-0 flex-1 items-start justify-between gap-3',
      variant === 'tech' && 'group-option-item--tech'
    ]"
  >
    <!-- Left: name + description -->
    <div
      class="flex min-w-0 flex-1 flex-col items-start"
      :title="description || undefined"
    >
      <GroupBadge
        :name="name"
        :platform="platform"
        :subscription-type="subscriptionType"
        :show-rate="false"
        class="groupOptionItemBadge"
      />
      <span
        v-if="description"
        :class="[
          'mt-1.5 w-full whitespace-pre-line [overflow-wrap:anywhere] text-left text-xs leading-relaxed line-clamp-3',
          variant === 'tech'
            ? 'text-sky-700/70 dark:text-sky-200/55'
            : 'text-gray-500 dark:text-gray-400'
        ]"
      >
        {{ description }}
      </span>
    </div>

    <!-- Right: rate pill + checkmark -->
    <div class="flex shrink-0 items-center gap-2 pt-0.5">
      <div class="flex shrink-0 flex-col items-end gap-1">
        <span
          v-if="rateMultiplier !== undefined"
          :class="[
            'inline-flex items-center whitespace-nowrap px-2.5 py-1 text-xs font-semibold tabular-nums',
            variant === 'tech' ? 'tech-rate-pill' : ['rounded-full', ratePillClass]
          ]"
        >
          <template v-if="hasCustomRate">
            <span class="mr-1 line-through opacity-50">{{ rateMultiplier }}x</span>
            <span class="font-bold">{{ userRateMultiplier }}x</span>
          </template>
          <template v-else>
            {{ rateMultiplier }}x {{ t('admin.groups.rateLabel') }}
          </template>
        </span>
        <span
          v-if="hasPeakRate"
          :class="[
            'inline-flex items-center whitespace-nowrap px-2.5 py-1 text-xs font-semibold',
            variant === 'tech'
              ? 'tech-peak-pill'
              : 'rounded-full bg-amber-50 text-amber-700 dark:bg-amber-900/20 dark:text-amber-300'
          ]"
          :title="peakRateTitle"
        >
          {{ peakRateText }}
        </span>
      </div>
      <svg
        v-if="showCheckmark && selected"
        :class="[
          'h-4 w-4 shrink-0',
          variant === 'tech'
            ? 'text-sky-500 dark:text-cyan-300'
            : 'text-primary-600 dark:text-primary-400'
        ]"
        fill="none"
        stroke="currentColor"
        viewBox="0 0 24 24"
        stroke-width="2.25"
      >
        <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
      </svg>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import GroupBadge from './GroupBadge.vue'
import type { SubscriptionType, GroupPlatform } from '@/types'
import { useAppStore } from '@/stores/app'
import { formatPeakRateWindow, serverTimezoneLabel } from '@/utils/peak-rate'

const { t } = useI18n()

interface Props {
  name: string
  platform: GroupPlatform
  subscriptionType?: SubscriptionType
  rateMultiplier?: number
  userRateMultiplier?: number | null
  peakRateEnabled?: boolean
  peakStart?: string
  peakEnd?: string
  peakRateMultiplier?: number
  description?: string | null
  selected?: boolean
  showCheckmark?: boolean
  /** tech: cyber-blue styling for API key group pickers */
  variant?: 'default' | 'tech'
}

const props = withDefaults(defineProps<Props>(), {
  subscriptionType: 'standard',
  selected: false,
  showCheckmark: true,
  userRateMultiplier: null,
  peakRateEnabled: false,
  variant: 'default'
})

const hasCustomRate = computed(() => {
  return (
    props.userRateMultiplier !== null &&
    props.userRateMultiplier !== undefined &&
    props.rateMultiplier !== undefined &&
    props.userRateMultiplier !== props.rateMultiplier
  )
})

const appStore = useAppStore()

const hasPeakRate = computed(() => {
  return Boolean(props.peakRateEnabled && props.peakStart && props.peakEnd)
})

const peakRateText = computed(() => {
  return formatPeakRateWindow(
    {
      peak_rate_enabled: props.peakRateEnabled,
      peak_start: props.peakStart,
      peak_end: props.peakEnd,
      peak_rate_multiplier: props.peakRateMultiplier
    },
    serverTimezoneLabel(appStore.cachedPublicSettings?.server_utc_offset)
  )
})

const peakRateTitle = computed(() => {
  return t('common.peakRateTooltip', { window: peakRateText.value })
})

const ratePillClass = computed(() => {
  switch (props.platform) {
    case 'anthropic':
      return 'bg-amber-50 text-amber-700 dark:bg-amber-900/20 dark:text-amber-400'
    case 'openai':
      return 'bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-400'
    case 'gemini':
      return 'bg-sky-50 text-sky-700 dark:bg-sky-900/20 dark:text-sky-400'
    default:
      return 'bg-violet-50 text-violet-700 dark:bg-violet-900/20 dark:text-violet-400'
  }
})
</script>

<style scoped>
.groupOptionItemBadge :deep(span.truncate) {
  font-weight: 600;
}

.group-option-item--tech .groupOptionItemBadge :deep(span.truncate) {
  font-weight: 600;
  letter-spacing: 0.01em;
}

.tech-rate-pill {
  border-radius: 0.375rem;
  border: 1px solid rgb(14 165 233 / 0.35);
  background: linear-gradient(135deg, rgb(224 242 254 / 0.95), rgb(186 230 253 / 0.55));
  color: rgb(3 105 161);
  box-shadow: inset 0 1px 0 rgb(255 255 255 / 0.55);
}

.tech-peak-pill {
  border-radius: 0.375rem;
  border: 1px solid rgb(56 189 248 / 0.3);
  background: rgb(14 165 233 / 0.1);
  color: rgb(3 105 161);
}
</style>

<style>
/* Dark-mode overrides kept unscoped: Vue scoped compiler drops :global(.dark) in production. */
.dark .tech-rate-pill {
  border-color: rgba(34, 211, 238, 0.35);
  background: linear-gradient(135deg, rgba(8, 47, 73, 0.95), rgba(12, 74, 110, 0.65));
  color: #7dd3fc;
  box-shadow: inset 0 1px 0 rgba(56, 189, 248, 0.14);
}

.dark .tech-peak-pill {
  border-color: rgba(34, 211, 238, 0.28);
  background: rgba(6, 182, 212, 0.16);
  color: #a5f3fc;
}
</style>
