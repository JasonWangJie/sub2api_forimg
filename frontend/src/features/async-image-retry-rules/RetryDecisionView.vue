<template>
  <div class="space-y-2 text-sm">
    <div class="flex flex-wrap items-center gap-2">
      <span class="rounded-full px-2.5 py-1 text-xs font-medium" :class="sourceClass">{{ t(`retryRules.sources.${decision.source || 'none'}`) }}</span>
      <span>{{ decision.action ? t(`retryRules.actions.${decision.action}`, decision.action) : t('retryRules.previewNoAction') }}</span>
      <span v-if="decision.delay_seconds" class="text-gray-500 dark:text-gray-400">{{ decision.delay_seconds }} s</span>
    </div>
    <p v-if="decision.rule_id" class="break-all font-mono text-xs text-gray-500 dark:text-gray-400">{{ decision.rule_id }}<span v-if="decision.step_id"> / {{ decision.step_id }}</span></p>
    <p v-if="decision.requested_action && decision.requested_action !== decision.action" class="text-xs text-gray-500 dark:text-gray-400">{{ t(`retryRules.actions.${decision.requested_action}`, decision.requested_action) }} → {{ decision.action ? t(`retryRules.actions.${decision.action}`, decision.action) : '–' }}</p>
    <p v-if="decision.block_reason" class="break-words text-amber-700 dark:text-amber-300">{{ t('retryRules.blockReason') }} · {{ t(`retryRules.blockReasons.${decision.block_reason}`, decision.block_reason) }}</p>
    <p v-if="decision.revision" class="text-xs text-gray-500 dark:text-gray-400">{{ t('retryRules.revision', { revision: decision.revision }) }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { RetryDecision } from '@/api/admin/asyncImageRetryRules'

const props = defineProps<{ decision: RetryDecision }>()
const { t } = useI18n()
const sourceClass = computed(() => ({
  custom: 'bg-primary-100 text-primary-700 dark:bg-primary-950/50 dark:text-primary-300',
  builtin: 'bg-sky-100 text-sky-700 dark:bg-sky-950/50 dark:text-sky-300',
  none: 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300',
})[props.decision.source] || 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300')
</script>
