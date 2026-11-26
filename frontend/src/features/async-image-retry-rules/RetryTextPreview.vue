<template>
  <BaseDialog :show="true" :title="t('retryRules.textPreview')" width="wide" @close="emit('close')">
    <form id="async-retry-preview-form" class="space-y-4" @submit.prevent="run">
      <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('retryRules.previewHint') }}</p>
      <div class="grid gap-4 sm:grid-cols-3">
        <label v-for="field in textFields" :key="field.key"><span class="input-label">{{ t(`retryRules.${field.label}`) }}</span><input v-model.trim="text[field.key]" class="input" maxlength="180" /></label>
        <label><span class="input-label">{{ t('retryRules.stage') }}</span><select v-model="stage" class="input"><option value="">–</option><option v-for="option in retryStages" :key="option" :value="option">{{ t(`retryRules.stages.${option}`) }}</option></select></label>
        <label v-for="field in numericFields" :key="field.key"><span class="input-label">{{ t(`retryRules.${field.label}`) }}</span><input v-model="numbers[field.key]" type="number" :min="field.min" :max="field.max" class="input" /></label>
      </div>
      <label class="block"><span class="input-label">{{ t('retryRules.previewMessage') }}</span><textarea v-model.trim="message" class="input" rows="4" maxlength="2048" /></label>
      <p v-if="error" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ error }}</p>
      <RetryPreviewView v-if="result" :preview="result" />
    </form>
    <template #footer>
      <div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.close') }}</button><button type="submit" form="async-retry-preview-form" class="btn btn-primary" :disabled="loading">{{ t('retryRules.textPreview') }}</button></div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { asyncImageRetryRulesAPI, retryStages, type RetryConfig, type RetryFact, type RetryPreview, type RetryStage } from '@/api/admin/asyncImageRetryRules'
import RetryPreviewView from './RetryPreviewView.vue'
import { cloneDraft } from './draft'

const props = defineProps<{ draft: RetryConfig }>()
const emit = defineEmits<{ close: [] }>()
const { t } = useI18n()
const textFields = [
  { key: 'platform', label: 'platform' }, { key: 'model', label: 'model' },
  { key: 'category', label: 'category' }, { key: 'error_code', label: 'errorCode' },
] as const
const numericFields = [
  { key: 'http_status', label: 'httpStatus', min: 100, max: 599 },
  { key: 'group_id', label: 'groupId', min: 1, max: Number.MAX_SAFE_INTEGER },
  { key: 'account_id', label: 'accountId', min: 1, max: Number.MAX_SAFE_INTEGER },
] as const
const text = reactive({ platform: '', model: '', category: '', error_code: '' })
const numbers = reactive<Record<typeof numericFields[number]['key'], string | number>>({ http_status: '', group_id: '', account_id: '' })
const stage = ref<RetryStage | ''>('')
const message = ref('')
const result = ref<RetryPreview | null>(null)
const error = ref('')
const loading = ref(false)
let controller: AbortController | undefined

async function run() {
  const fact: RetryFact = {}
  for (const { key } of textFields) if (text[key]) fact[key] = text[key]
  for (const { key } of numericFields) if (numbers[key] !== '') fact[key] = Number(numbers[key])
  if (stage.value) fact.stage = stage.value
  if (message.value) fact.message = message.value
  if (!Object.keys(fact).length) {
    error.value = t('retryRules.requiresText')
    return
  }
  controller?.abort()
  const current = new AbortController()
  controller = current
  loading.value = true
  error.value = ''
  result.value = null
  try {
    const preview = await asyncImageRetryRulesAPI.preview({ draft: cloneDraft(props.draft), fact }, current.signal)
    if (!current.signal.aborted) result.value = preview
  } catch (err) {
    if (!current.signal.aborted) error.value = (err as { message?: string }).message || t('retryRules.previewError')
  } finally {
    if (controller === current) loading.value = false
  }
}

onUnmounted(() => controller?.abort())
</script>
