<template>
  <div class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-700" aria-live="polite">
    <div class="grid gap-4" :class="preview.historical && 'sm:grid-cols-2'">
      <div v-if="preview.historical" class="space-y-2">
        <h3 class="font-medium">{{ t('retryRules.historical') }}</h3>
        <RetryDecisionView :decision="preview.historical" />
      </div>
      <div class="space-y-2">
        <h3 class="font-medium">{{ t('retryRules.decision') }}</h3>
        <RetryDecisionView :decision="preview.decision" />
      </div>
    </div>
    <div class="space-y-1 text-sm text-gray-600 dark:text-gray-300">
      <p>{{ t('retryRules.eligible') }} · {{ t(`retryRules.eligibility.${preview.eligibility}`, preview.eligibility) }}</p>
      <p v-if="preview.eligibility_reason" class="break-words">{{ t(`retryRules.blockReasons.${preview.eligibility_reason}`, preview.eligibility_reason) }}</p>
      <p v-if="preview.candidate_account_ids?.length">{{ t('retryRules.targetCandidates') }} · {{ preview.candidate_account_ids.join(', ') }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { RetryPreview } from '@/api/admin/asyncImageRetryRules'
import RetryDecisionView from './RetryDecisionView.vue'

defineProps<{ preview: RetryPreview }>()
const { t } = useI18n()
</script>
