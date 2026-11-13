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
        <p v-if="loading" role="status" class="mt-3 text-sm text-gray-500">{{ t('admin.accounts.rateLimit.loading') }}</p>
        <dl v-else-if="preview && limits.length" class="mt-3 space-y-3 text-sm">
          <div v-for="(limit, index) in limits" :key="index" class="flex flex-col gap-1 sm:flex-row sm:justify-between sm:gap-4">
            <dt class="break-words text-gray-600 dark:text-gray-400">
              {{ t(`admin.accounts.rateLimit.kinds.${limit.kind}`) }}<span v-if="limit.scope"> · {{ limit.scope }}</span>
            </dt>
            <dd class="shrink-0 tabular-nums text-gray-900 dark:text-gray-200">
              {{ limit.until ? formatDateTime(limit.until) : t('admin.accounts.rateLimit.unknownReset') }}
            </dd>
          </div>
        </dl>
        <p v-if="preview?.truncated" class="mt-2 text-xs text-amber-700 dark:text-amber-300">{{ t('admin.accounts.rateLimit.truncated') }}</p>
        <p v-if="!result && !loading && preview && !limits.length" class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.accounts.rateLimit.noActiveLimit') }}</p>
      </div>
      <div v-if="result" data-testid="clear-result" class="rounded-lg border p-3 text-sm leading-6" :class="result.cache_sync.status === 'warning' ? 'border-amber-200 bg-amber-50 text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-200' : 'border-emerald-200 bg-emerald-50 text-emerald-800 dark:border-emerald-800 dark:bg-emerald-900/20 dark:text-emerald-200'" role="status">
        <p class="font-medium">{{ t('admin.accounts.rateLimit.resultSummary', { before: result.before.active_count, after: result.after.active_count }) }}</p>
        <p>{{ t(result.cache_sync.status === 'warning' ? 'admin.accounts.rateLimit.cacheWarning' : 'admin.accounts.rateLimit.cacheSynced') }}</p>
        <p v-if="result.cache_sync.failed_components.length">{{ t('admin.accounts.rateLimit.cacheComponents') }}: {{ result.cache_sync.failed_components.map(cacheComponentLabel).join(' · ') }}</p>
        <details v-if="result.before.restrictions.length" class="mt-2">
          <summary class="cursor-pointer">{{ t('admin.accounts.rateLimit.beforeSummary') }}</summary>
          <ul class="mt-1 space-y-1">
            <li v-for="(item, index) in result.before.restrictions" :key="index" class="break-words">{{ t(`admin.accounts.rateLimit.kinds.${item.kind}`) }}<span v-if="item.scope"> · {{ item.scope }}</span> · {{ item.until ? formatDateTime(item.until) : t('admin.accounts.rateLimit.unknownReset') }}</li>
          </ul>
        </details>
      </div>
      <p v-if="!result" class="text-sm leading-6 text-gray-600 dark:text-gray-400">{{ t('admin.accounts.rateLimit.scopeHint') }}</p>
      <p v-if="preview && !preview.schedulable" data-testid="scheduling-paused" class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-sm leading-6 text-amber-800 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-200">
        {{ t('admin.accounts.rateLimit.schedulingPaused') }}
      </p>
      <p v-if="preview && preview.status !== 'active'" class="text-sm text-amber-700 dark:text-amber-300">{{ t('admin.accounts.rateLimit.accountInactive') }}</p>
      <div class="border-t border-gray-100 pt-4 dark:border-dark-700">
        <p class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ t('admin.accounts.rateLimit.settingsPath') }}</p>
        <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.accounts.rateLimit.settingsHint') }}</p>
      </div>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300">{{ error }}</p>
    </div>
    <template #footer>
      <div class="flex flex-wrap justify-end gap-3">
        <button type="button" class="btn btn-secondary" :disabled="submitting" data-testid="cancel" @click="close">{{ t(result ? 'common.close' : 'common.cancel') }}</button>
        <button v-if="!result && !loading" type="button" class="btn btn-secondary" :disabled="submitting" data-testid="reload-preview" @click="loadPreview">{{ t('admin.accounts.rateLimit.reload') }}</button>
        <button v-if="!result && (preview || loading)" type="button" class="btn btn-primary" :disabled="submitting || loading || !preview || !limits.length" data-testid="clear-rate-limit" @click="clear">
          {{ t(submitting ? 'admin.accounts.rateLimit.clearing' : 'admin.accounts.rateLimit.confirm') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useIntervalFn } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { clearRateLimit, getRateLimitState, type AccountRateLimitState, type AccountRateLimitClearResult } from '@/api/admin/accounts'
import { formatDateTime } from '@/utils/format'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { Account } from '@/types'

const props = defineProps<{ show: boolean; account: Account | null }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'cleared', result: AccountRateLimitClearResult): void }>()
const { t } = useI18n()
const submitting = ref(false)
const loading = ref(false)
const preview = ref<AccountRateLimitState | null>(null)
const result = ref<AccountRateLimitClearResult | null>(null)
const error = ref('')
const now = ref(Date.now())
let generation = 0
const limits = computed(() => (preview.value?.restrictions ?? []).filter(item => !item.until || Date.parse(item.until) > now.value))
const { pause, resume } = useIntervalFn(() => { now.value = Date.now() }, 1000, { immediate: false })
const cacheComponentLabel = (name: string) => t(`admin.accounts.rateLimit.cache.${['temp_unsched', 'openai_403', 'scheduler_snapshot', 'account_refresh', 'runtime_block'].includes(name) ? name : 'unknown'}`)

const loadPreview = async () => {
  if (!props.show || !props.account || submitting.value) return
  const current = ++generation
  const id = props.account.id
  preview.value = null
  error.value = ''
  loading.value = true
  try {
    const state = await getRateLimitState(id)
    if (current === generation && props.show && props.account?.id === id) preview.value = state
  } catch (cause) {
    if (current === generation) error.value = extractApiErrorMessage(cause, t('admin.accounts.rateLimit.loadFailed'))
  } finally {
    if (current === generation) loading.value = false
  }
}
watch([() => props.show, () => props.account?.id], () => {
  ++generation
  preview.value = null
  result.value = null
  loading.value = false
  error.value = ''
  now.value = Date.now()
  if (props.show) { resume(); void loadPreview() }
  else pause()
}, { immediate: true })

const close = () => { if (!submitting.value) emit('close') }
const clear = async () => {
  now.value = Date.now()
  if (submitting.value || loading.value || result.value || !props.show || !props.account || !preview.value || !limits.value.length) return
  submitting.value = true
  const current = generation
  const id = props.account.id
  error.value = ''
  try {
    const updated = await clearRateLimit(id, preview.value.version)
    if (current === generation && props.show && props.account?.id === id) {
      result.value = updated
      preview.value = updated.after
    }
    emit('cleared', updated)
  } catch (cause) {
    if (current !== generation || !props.show || props.account?.id !== id) return
    const apiError = cause as { status?: number; code?: string; response?: { status?: number } }
    if (apiError.status === 409 || apiError.response?.status === 409 || apiError.code === 'RATE_LIMIT_STATE_CHANGED') {
      preview.value = null
      error.value = t('admin.accounts.rateLimit.changed')
    } else error.value = extractApiErrorMessage(cause, t('admin.accounts.rateLimit.failed'))
  } finally {
    submitting.value = false
  }
}
</script>
