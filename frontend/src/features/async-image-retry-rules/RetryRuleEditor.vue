<template>
  <BaseDialog :show="true" :title="form.name || t('retryRules.newRule')" width="wide" @close="emit('close')">
    <form id="async-retry-rule-form" class="space-y-5" @submit.prevent="submit">
      <div v-if="sampleMessage" class="rounded-lg bg-gray-100 p-3 text-sm dark:bg-dark-800">
        <h3 class="font-medium">{{ t('retryRules.previewMessage') }}</h3>
        <p class="mt-1 whitespace-pre-wrap break-words text-gray-600 dark:text-gray-300">{{ sampleMessage }}</p>
      </div>
      <div class="grid gap-4 sm:grid-cols-2">
        <label><span class="input-label">{{ t('retryRules.name') }}</span><input v-model.trim="form.name" class="input" required maxlength="120" /></label>
        <label><span class="input-label">{{ t('retryRules.label') }}</span><input v-model.trim="form.category" class="input" maxlength="80" /></label>
        <label class="flex items-center gap-2 text-sm"><input v-model="form.enabled" type="checkbox" class="rounded" />{{ t('retryRules.enabled') }}</label>
      </div>
      <fieldset class="rounded-xl border border-gray-200 p-4 dark:border-dark-700">
        <legend class="px-2 text-sm font-semibold">{{ t('retryRules.scope') }}</legend>
        <div class="grid gap-4 sm:grid-cols-2">
          <div class="sm:col-span-2">
            <div class="input-label">{{ t('retryRules.platform') }}</div>
            <div class="flex flex-wrap gap-3">
              <label v-for="platform in platforms" :key="platform.value" class="flex items-center gap-2 text-sm"><input v-model="form.scope.platforms" type="checkbox" class="rounded" :value="platform.value" />{{ platform.label }}</label>
            </div>
          </div>
          <label v-for="field in scopeFields" :key="field.key"><span class="input-label">{{ t(`retryRules.${field.label}`) }}</span><textarea v-model="values[field.key]" class="input" rows="2" :placeholder="t(field.numeric ? 'retryRules.idListHint' : 'retryRules.listHint')" /></label>
          <div class="sm:col-span-2">
            <div class="input-label">{{ t('retryRules.stage') }}</div>
            <div class="flex flex-wrap gap-3">
              <label v-for="stage in retryStages" :key="stage" class="flex items-center gap-2 text-sm"><input v-model="form.scope.stages" type="checkbox" class="rounded" :value="stage" />{{ t(`retryRules.stages.${stage}`) }}</label>
            </div>
          </div>
        </div>
      </fieldset>
      <fieldset class="rounded-xl border border-gray-200 p-4 dark:border-dark-700">
        <legend class="px-2 text-sm font-semibold">{{ t('retryRules.match') }}</legend>
        <div class="grid gap-4 sm:grid-cols-2">
          <label v-for="field in matchFields" :key="field.key"><span class="input-label">{{ t(`retryRules.${field.label}`) }}</span><textarea v-model="values[field.key]" rows="2" class="input" :placeholder="t('retryRules.listHint')" /></label>
          <label><span class="input-label">{{ t('retryRules.regex') }}</span><input v-model.trim="form.match.regex" class="input font-mono" maxlength="2048" autocomplete="off" /></label>
        </div>
      </fieldset>
      <fieldset class="rounded-xl border border-gray-200 p-4 dark:border-dark-700">
        <legend class="px-2 text-sm font-semibold">{{ t('retryRules.targets') }}</legend>
        <div class="grid gap-4 sm:grid-cols-2">
          <label><span class="input-label">{{ t('retryRules.allowAccounts') }}</span><textarea v-model="values.allowed_account_ids" class="input" rows="2" :placeholder="t('retryRules.idListHint')" /></label>
          <label><span class="input-label">{{ t('retryRules.excludeAccounts') }}</span><textarea v-model="values.excluded_account_ids" class="input" rows="2" :placeholder="t('retryRules.idListHint')" /></label>
        </div>
      </fieldset>
      <fieldset class="rounded-xl border border-gray-200 p-4 dark:border-dark-700">
        <legend class="px-2 text-sm font-semibold">{{ t('retryRules.steps') }}</legend>
        <div class="space-y-3">
          <div v-for="(step, index) in form.steps" :key="step.id" class="rounded-lg bg-gray-50 p-3 dark:bg-dark-800/70">
            <div class="grid gap-3 sm:grid-cols-2">
              <label><span class="input-label">{{ index + 1 }}. {{ t('retryRules.action') }}</span><select v-model="step.action" class="input" required><option v-for="action in retryActions" :key="action" :value="action">{{ t(`retryRules.actions.${action}`) }}</option></select></label>
              <label><span class="input-label">{{ t('retryRules.maxAttempts') }}</span><input v-model.number="step.max_attempts" type="number" min="1" max="16" class="input" required /></label>
              <label><span class="input-label">{{ t('retryRules.delay') }}</span><input v-model.number="step.delay_seconds" type="number" min="0" max="1800" class="input" required /></label>
              <label><span class="input-label">{{ t('retryRules.backoff') }}</span><select v-model="step.backoff" class="input"><option value="fixed">{{ t('retryRules.fixed') }}</option><option value="exponential">{{ t('retryRules.exponential') }}</option></select></label>
              <label><span class="input-label">{{ t('retryRules.maxDelay') }}</span><input v-model.number="step.max_delay_seconds" type="number" min="0" max="1800" class="input" required /></label>
              <div class="flex flex-wrap items-end gap-2">
                <button type="button" class="btn btn-secondary btn-sm" :disabled="index === 0" @click="moveStep(index, -1)">{{ t('retryRules.moveUp') }}</button>
                <button type="button" class="btn btn-secondary btn-sm" :disabled="index === form.steps.length - 1" @click="moveStep(index, 1)">{{ t('retryRules.moveDown') }}</button>
                <button type="button" class="btn btn-ghost btn-sm text-red-600" :disabled="form.steps.length < 2" @click="form.steps.splice(index, 1)">{{ t('common.delete') }}</button>
              </div>
            </div>
          </div>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="form.steps.length >= 8" @click="form.steps.push(newStep())">{{ t('retryRules.addStep') }}</button>
          <div class="grid gap-4 sm:grid-cols-2">
            <label><span class="input-label">{{ t('retryRules.ruleAttempts') }}</span><input v-model.number="form.max_attempts" type="number" min="0" max="128" class="input" required /><span class="input-hint">{{ t('retryRules.ruleAttemptsHint') }}</span></label>
            <label><span class="input-label">{{ t('retryRules.exhausted') }}</span><select v-model="form.on_exhausted" class="input"><option value="manual_review">{{ t('retryRules.actions.manual_review') }}</option><option value="stop">{{ t('retryRules.actions.stop') }}</option></select></label>
          </div>
        </div>
      </fieldset>
      <p v-if="error" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ error }}</p>
    </form>
    <template #footer>
      <div class="flex justify-end gap-2">
        <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.cancel') }}</button>
        <button type="submit" form="async-retry-rule-form" class="btn btn-primary">{{ t('retryRules.applyDraft') }}</button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { retryActions, retryStages, type RetryRule } from '@/api/admin/asyncImageRetryRules'
import { cloneDraft, newStep } from './draft'

const props = defineProps<{ rule: RetryRule; sampleMessage?: string }>()
const emit = defineEmits<{ close: []; apply: [rule: RetryRule] }>()
const { t } = useI18n()
const form = reactive(cloneDraft(props.rule))
const error = ref('')
const platforms = [{ value: 'openai', label: 'OpenAI' }, { value: 'gemini', label: 'Gemini' }]

const scopeFields = [
  { key: 'models', label: 'model', numeric: false },
  { key: 'group_ids', label: 'groupId', numeric: true }, { key: 'source_account_ids', label: 'accountId', numeric: true },
  { key: 'categories', label: 'category', numeric: false },
] as const
const matchFields = [
  { key: 'http_statuses', label: 'httpStatus' }, { key: 'error_codes', label: 'errorCode' },
  { key: 'contains_any', label: 'containsAny' }, { key: 'contains_all', label: 'containsAll' },
  { key: 'excludes', label: 'excludes' },
] as const
const values = reactive({
  ...Object.fromEntries(scopeFields.map(({ key }) => [key, form.scope[key]?.join('\n') || ''])),
  ...Object.fromEntries(matchFields.map(({ key }) => [key, form.match[key]?.join('\n') || ''])),
  allowed_account_ids: form.allowed_account_ids?.join('\n') || '',
  excluded_account_ids: form.excluded_account_ids?.join('\n') || '',
}) as Record<typeof scopeFields[number]['key'] | typeof matchFields[number]['key'] | 'allowed_account_ids' | 'excluded_account_ids', string>

form.scope.stages ??= []
form.scope.platforms ??= []
form.match.regex ??= ''
for (const step of form.steps) step.max_delay_seconds ??= 0

function splitLines(value: string, delimiter = /\r?\n/) {
  return [...new Set(value.split(delimiter).map(part => part.trim()).filter(Boolean))]
}

function numberList(value: string) {
  const values = splitLines(value, /[,\s，]+/)
  if (!values.every(part => /^\d+$/.test(part) && Number.isSafeInteger(Number(part)) && Number(part) > 0)) {
    throw new Error(t('retryRules.invalidNumberList'))
  }
  return [...new Set(values.map(Number))]
}

function moveStep(index: number, delta: -1 | 1) {
  const [step] = form.steps.splice(index, 1)
  form.steps.splice(index + delta, 0, step)
}

function submit() {
  const rule = cloneDraft(form)
  try {
    rule.scope.models = splitLines(values.models)
    rule.scope.group_ids = numberList(values.group_ids)
    rule.scope.source_account_ids = numberList(values.source_account_ids)
    rule.scope.categories = splitLines(values.categories)
    rule.match.http_statuses = numberList(values.http_statuses)
    rule.match.error_codes = splitLines(values.error_codes)
    rule.match.contains_any = splitLines(values.contains_any)
    rule.match.contains_all = splitLines(values.contains_all)
    rule.match.excludes = splitLines(values.excludes)
    rule.allowed_account_ids = numberList(values.allowed_account_ids)
    rule.excluded_account_ids = numberList(values.excluded_account_ids)
  } catch (err) {
    error.value = (err as Error).message
    return
  }
  const { match, scope } = rule
  if (!rule.name || !(match.http_statuses.length || match.error_codes.length || match.contains_any.length ||
    match.contains_all.length || match.regex || scope.categories.length)) {
    error.value = t('retryRules.positiveRequired')
    return
  }
  emit('apply', rule)
}
</script>
