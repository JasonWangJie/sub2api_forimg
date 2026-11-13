<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.rateLimit.title')"
    :close-on-escape="!submitting"
    :show-close-button="!submitting"
    @close="close"
  >
    <div v-if="account" class="space-y-4">
      <div class="rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-900/50">
        <p class="break-words font-medium text-gray-900 dark:text-white">{{ account.name }}</p>
        <dl v-if="limits.length" class="mt-3 space-y-3 text-sm">
          <div v-for="(limit, index) in limits" :key="index" class="flex flex-col gap-1 sm:flex-row sm:justify-between sm:gap-4">
            <dt class="break-words text-gray-600 dark:text-gray-400">
              {{ t(`admin.accounts.rateLimit.kinds.${limit.kind}`) }}<span v-if="limit.scope"> · {{ limit.scope }}</span>
            </dt>
            <dd class="shrink-0 tabular-nums text-gray-900 dark:text-gray-200">
              {{ limit.until ? formatDateTime(limit.until) : t('admin.accounts.rateLimit.unknownReset') }}
            </dd>
          </div>
        </dl>
        <p v-else class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.accounts.rateLimit.noActiveLimit') }}</p>
      </div>
      <p class="text-sm leading-6 text-gray-600 dark:text-gray-400">{{ t('admin.accounts.rateLimit.scopeHint') }}</p>
      <p v-if="!account.schedulable" data-testid="scheduling-paused" class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm leading-6 text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-200">
        {{ t('admin.accounts.rateLimit.schedulingPaused') }}
      </p>
      <p v-if="account.status !== 'active'" class="text-sm text-amber-700 dark:text-amber-300">{{ t('admin.accounts.rateLimit.accountInactive') }}</p>
      <div class="border-t border-gray-100 pt-4 dark:border-dark-700">
        <p class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ t('admin.accounts.rateLimit.settingsPath') }}</p>
        <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.accounts.rateLimit.settingsHint') }}</p>
      </div>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
    </div>
    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" :disabled="submitting" data-testid="cancel" @click="close">{{ t('common.cancel') }}</button>
        <button type="button" class="btn btn-primary" :disabled="submitting || !account || !limits.length" data-testid="clear-rate-limit" @click="clear">
          {{ t(submitting ? 'admin.accounts.rateLimit.clearing' : 'admin.accounts.rateLimit.confirm') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { clearRateLimit } from '@/api/admin/accounts'
import { formatDateTime } from '@/utils/format'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { Account } from '@/types'
import { getAccountRateLimitEntries } from './accountRateLimits'

const props = defineProps<{ show: boolean; account: Account | null }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'cleared', account: Account): void }>()
const { t } = useI18n()
const submitting = ref(false)
const error = ref('')
const limits = computed(() => getAccountRateLimitEntries(props.account))

watch(() => [props.show, props.account?.id], () => { error.value = '' })
const close = () => { if (!submitting.value) emit('close') }
const clear = async () => {
  if (submitting.value || !props.show || !props.account || !limits.value.length) return
  submitting.value = true
  error.value = ''
  try {
    const updated = await clearRateLimit(props.account.id)
    emit('cleared', updated)
    emit('close')
  } catch (cause) {
    error.value = extractApiErrorMessage(cause, t('admin.accounts.rateLimit.failed'))
  } finally {
    submitting.value = false
  }
}
</script>
