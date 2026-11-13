<template>
  <BaseDialog :show="show" :title="t('admin.accounts.imageResolutionGuard.title')" :close-on-escape="!clearing" :close-on-click-outside="!clearing" :show-close-button="!clearing" @close="close">
    <div v-if="account" class="space-y-4">
      <div>
        <p class="break-words font-medium text-gray-900 dark:text-white">{{ account.name }}</p>
        <p class="mt-1 text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('admin.accounts.imageResolutionGuard.scopeHint') }}</p>
      </div>
      <p v-if="loading" role="status" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
      <div v-else-if="loaded && !activeItems.length" class="rounded-xl border border-dashed border-gray-200 bg-gray-50 px-4 py-8 text-center text-sm text-gray-500 dark:border-dark-600 dark:bg-dark-900/50 dark:text-gray-400" data-testid="resolution-no-cooldowns">
        {{ t('admin.accounts.imageResolutionGuard.noCooldowns') }}
      </div>
      <ul v-else class="space-y-3">
        <li v-for="item in activeItems" :key="item.cycle_id" class="rounded-xl border border-amber-200 bg-amber-50/40 p-4 dark:border-amber-900 dark:bg-amber-950/20" data-testid="resolution-cooldown">
          <div class="flex flex-wrap items-start justify-between gap-2">
            <p class="min-w-0 break-all font-medium text-gray-900 dark:text-white">{{ item.model }}</p>
            <span class="rounded-md bg-amber-100 px-2 py-1 text-xs font-semibold text-amber-800 dark:bg-amber-900/50 dark:text-amber-200">{{ item.size_tier }}</span>
          </div>
          <p class="mt-2 text-sm leading-6 text-gray-600 dark:text-gray-300">{{ reasonLabel(item.reason) }}</p>
          <dl class="mt-3 grid gap-x-4 gap-y-2 text-xs sm:grid-cols-2">
            <div><dt class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.imageResolutionGuard.expected') }}</dt><dd class="mt-1 tabular-nums text-gray-800 dark:text-gray-200">{{ dimensions(item.requested_width, item.requested_height) }}</dd></div>
            <div><dt class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.imageResolutionGuard.actual') }}</dt><dd class="mt-1 tabular-nums text-gray-800 dark:text-gray-200">{{ dimensions(item.actual_width, item.actual_height) }}<span v-if="validPercent(item.area_percent)"> · {{ percent(item.area_percent!) }}%</span></dd></div>
            <div class="sm:col-span-2"><dt class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.imageResolutionGuard.until') }}</dt><dd class="mt-1 tabular-nums text-gray-800 dark:text-gray-200">{{ formatDateTime(item.cooldown_until) }}</dd></div>
          </dl>
          <div v-if="confirming === item.cycle_id" class="mt-4 space-y-3 border-t border-amber-200 pt-3 dark:border-amber-900" data-testid="resolution-clear-confirmation">
            <p class="text-xs leading-5 text-amber-800 dark:text-amber-200">{{ t('admin.accounts.imageResolutionGuard.confirmHint') }}</p>
            <div class="flex flex-wrap justify-end gap-2">
              <button type="button" class="btn btn-secondary btn-sm" :disabled="clearing" data-testid="resolution-cancel-clear" @click="confirming = null">{{ t('common.cancel') }}</button>
              <button type="button" class="btn btn-primary btn-sm" :disabled="clearing" data-testid="resolution-confirm-clear" @click="clear(item)">{{ t(clearing ? 'admin.accounts.imageResolutionGuard.clearing' : 'admin.accounts.imageResolutionGuard.confirm') }}</button>
            </div>
          </div>
          <div v-else class="mt-4 flex justify-end">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="clearing || loading" data-testid="resolution-request-clear" @click="confirming = item.cycle_id">{{ t('admin.accounts.imageResolutionGuard.clear') }}</button>
          </div>
        </li>
      </ul>
      <p class="rounded-lg bg-gray-50 p-3 text-xs leading-5 text-gray-500 dark:bg-dark-900/50 dark:text-gray-400">{{ t('admin.accounts.imageResolutionGuard.settingsHint') }}</p>
      <p v-if="success" role="status" class="text-sm text-emerald-700 dark:text-emerald-300">{{ t('admin.accounts.imageResolutionGuard.cleared') }}</p>
      <p v-if="error" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ error }}</p>
    </div>
    <template #footer>
      <div class="flex flex-wrap justify-end gap-3">
        <button type="button" class="btn btn-secondary" :disabled="loading || clearing" data-testid="resolution-refresh" @click="load">{{ t('admin.accounts.imageResolutionGuard.refresh') }}</button>
        <button type="button" class="btn btn-primary" :disabled="clearing" data-testid="resolution-close" @click="close">{{ t('common.close') }}</button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useIntervalFn } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { clearImageResolutionCooldown, getImageResolutionCooldowns, type ImageResolutionCooldown } from '@/api/admin/imageResolutionGuard'
import type { Account } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime } from '@/utils/format'

const props = defineProps<{ show: boolean; account: Account | null }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'cleared'): void }>()
const { t } = useI18n()
const items = ref<ImageResolutionCooldown[]>([])
const loading = ref(false)
const loaded = ref(false)
const clearing = ref(false)
const confirming = ref<string | null>(null)
const success = ref(false)
const error = ref('')
const now = ref(Date.now())
let generation = 0
const activeItems = computed(() => items.value.filter(item => Number.isFinite(Date.parse(item.cooldown_until)) && Date.parse(item.cooldown_until) > now.value))
const { pause, resume } = useIntervalFn(() => { now.value = Date.now() }, 1000, { immediate: false })
const dimensions = (width?: number, height?: number) => width && height && width > 0 && height > 0 ? `${width} × ${height}` : t('admin.accounts.imageResolutionGuard.unknownDimensions')
const validPercent = (value?: number) => typeof value === 'number' && Number.isFinite(value) && value >= 0
const percent = (value: number) => Number(value.toFixed(2))
const reasonLabel = (value: string) => !value || value === 'image_resolution_downgrade' ? t('admin.accounts.imageResolutionGuard.defaultReason') : value

async function load() {
  if (!props.show || !props.account || clearing.value) return
  const current = ++generation
  const accountID = props.account.id
  confirming.value = null
  error.value = ''
  loading.value = true
  try {
    const result = await getImageResolutionCooldowns(accountID)
    if (current !== generation || !props.show || props.account?.id !== accountID) return
    items.value = result
    now.value = Date.now()
    loaded.value = true
  } catch (cause) {
    if (current === generation) error.value = extractApiErrorMessage(cause, t('admin.accounts.imageResolutionGuard.loadFailed'))
  } finally {
    if (current === generation) loading.value = false
  }
}

async function clear(item: ImageResolutionCooldown) {
  now.value = Date.now()
  if (!props.show || !props.account || loading.value || clearing.value || confirming.value !== item.cycle_id || !activeItems.value.some(value => value.cycle_id === item.cycle_id)) return
  const accountID = props.account.id
  const current = generation
  clearing.value = true
  error.value = ''
  success.value = false
  try {
    await clearImageResolutionCooldown(accountID, item.cycle_id)
    if (current !== generation || props.account?.id !== accountID) return
    items.value = items.value.filter(value => value.cycle_id !== item.cycle_id)
    confirming.value = null
    success.value = true
    emit('cleared')
  } catch (cause) {
    if (current === generation) error.value = extractApiErrorMessage(cause, t('admin.accounts.imageResolutionGuard.clearFailed'))
  } finally {
    clearing.value = false
  }
  if (success.value && current === generation) await load()
}

function close() { if (!clearing.value) emit('close') }

watch(() => [props.show, props.account?.id], () => {
  ++generation
  items.value = []
  confirming.value = null
  error.value = ''
  success.value = false
  loaded.value = false
  loading.value = false
  now.value = Date.now()
  if (props.show) { resume(); void load() }
  else pause()
}, { immediate: true })
</script>
