<template>
  <AppLayout>
    <div class="mx-auto max-w-[1500px] space-y-5 pb-8">
      <h1 class="text-xl font-semibold leading-tight text-gray-950 dark:text-white lg:hidden">{{ t('retryRules.title') }}</h1>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <nav class="tabs inline-flex" :aria-label="t('retryRules.title')">
          <button v-for="tab in tabs" :key="tab" type="button" class="tab" :class="{ 'tab-active': activeTab === tab }" :aria-current="activeTab === tab ? 'page' : undefined" :data-test="`retry-tab-${tab}`" @click="activeTab = tab">{{ t(`retryRules.${tab}`) }}</button>
        </nav>
        <div class="flex flex-wrap items-center gap-3">
          <span v-if="dirty" data-test="retry-unsaved" class="text-sm text-amber-700 dark:text-amber-300" role="status">{{ t('retryRules.draft') }}</span>
          <span v-if="active" class="rounded-md border border-gray-200 px-3 py-2 font-mono text-xs text-gray-600 dark:border-dark-700 dark:text-gray-300">{{ t('retryRules.revision', { revision: active.revision }) }}</span>
        </div>
      </div>

      <div v-if="loadError || conflict" role="alert" class="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-amber-200 bg-amber-50 p-4 text-sm text-amber-900 dark:border-amber-900/70 dark:bg-amber-950/40 dark:text-amber-200">
        <p>{{ conflict ? t('retryRules.conflict') : loadError }}</p>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || saving" @click="dirty ? showReload = true : loadConfig()">{{ t('retryRules.reload') }}</button>
      </div>

      <section v-if="activeTab === 'rules'" class="space-y-4" :aria-busy="loading">
        <template v-if="draft">
          <div class="card flex flex-wrap items-center justify-between gap-4 p-4 sm:p-5">
            <div>
              <label class="flex items-center gap-2 font-medium text-gray-900 dark:text-white"><input v-model="draft.pause_generation_retries" type="checkbox" class="rounded" :disabled="saving" data-test="retry-pause" />{{ t('retryRules.pause') }}</label>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('retryRules.pauseHelp') }}</p>
            </div>
            <div class="flex flex-wrap gap-2">
              <button type="button" class="btn btn-secondary" :disabled="!dirty || saving" @click="resetDraft">{{ t('common.reset') }}</button>
              <button type="button" data-test="save-retry-rules" class="btn btn-primary" :disabled="!dirty || saving || loading || conflict" @click="save">{{ saving ? t('common.saving') : t('retryRules.save') }}</button>
            </div>
          </div>
          <p v-if="saveError" role="alert" class="rounded-lg bg-red-50 p-3 text-sm text-red-800 dark:bg-red-950/40 dark:text-red-300">{{ saveError }}</p>

          <div class="card overflow-hidden">
            <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-200 p-4 dark:border-dark-700">
              <input v-model="search" class="input w-full sm:w-72" type="search" :aria-label="t('retryRules.search')" :placeholder="t('retryRules.search')" data-test="retry-search" />
              <div class="flex flex-wrap gap-2">
                <button type="button" class="btn btn-secondary btn-sm" :disabled="saving" @click="showTextPreview = true">{{ t('retryRules.textPreview') }}</button>
                <button type="button" class="btn btn-primary btn-sm" :disabled="saving" @click="editing = newRule()">{{ t('retryRules.newRule') }}</button>
              </div>
            </div>
            <p v-if="!visibleRules.length" class="p-8 text-center text-sm text-gray-500 dark:text-gray-400">{{ t(search.trim() ? 'retryRules.noMatchingRules' : 'retryRules.emptyRules') }}</p>
            <div v-else class="overflow-x-auto">
              <table class="w-full text-left text-sm">
                <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800 dark:text-gray-400"><tr><th scope="col" class="px-4 py-3">{{ t('retryRules.order') }}</th><th scope="col" class="px-4 py-3">{{ t('retryRules.name') }}</th><th scope="col" class="px-4 py-3">{{ t('retryRules.steps') }}</th><th scope="col" class="px-4 py-3">{{ t('common.status') }}</th><th scope="col" class="px-4 py-3">{{ t('common.actions') }}</th></tr></thead>
                <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                  <tr v-for="rule in visibleRules" :key="rule.id" :data-test="`retry-rule-${rule.id}`">
                    <td class="whitespace-nowrap px-4 py-3 align-top font-mono" data-test="retry-priority">{{ indexes.get(rule.id)! + 1 }}</td>
                    <td class="min-w-48 px-4 py-3 align-top">
                      <button type="button" class="text-left font-semibold text-primary-700 hover:underline focus-visible:underline dark:text-primary-300" :disabled="saving" @click="editing = rule">{{ rule.name }}</button>
                      <div class="mt-1 flex items-center gap-1.5">
                        <code class="select-all break-all text-xs text-gray-500 dark:text-gray-400">{{ rule.id }}</code>
                        <button type="button" class="btn btn-ghost btn-sm shrink-0" @click="copyRuleID(rule.id)">{{ t('retryRules.copyId') }}</button>
                      </div>
                    </td>
                    <td class="min-w-48 px-4 py-3 align-top">
                      <ol class="space-y-1 text-gray-700 dark:text-gray-300"><li v-for="step in rule.steps" :key="step.id">{{ t(`retryRules.actions.${step.action}`, step.action) }} <span class="font-mono text-xs">×{{ step.max_attempts }}</span></li></ol>
                    </td>
                    <td class="whitespace-nowrap px-4 py-3 align-top">
                      <label class="inline-flex items-center gap-2"><input v-model="rule.enabled" :disabled="saving" type="checkbox" class="rounded" />{{ t(rule.enabled ? 'retryRules.enabled' : 'retryRules.disabled') }}</label>
                    </td>
                    <td class="min-w-72 px-4 py-3 align-top">
                      <div class="flex flex-wrap gap-1.5">
                        <button type="button" class="btn btn-secondary btn-sm" :disabled="saving || indexes.get(rule.id) === 0" :aria-label="t('retryRules.up')" data-test="retry-move-up" @click="moveRule(draft, rule.id, -1)">{{ t('retryRules.moveUp') }}</button>
                        <button type="button" class="btn btn-secondary btn-sm" :disabled="saving || indexes.get(rule.id) === draft.rules.length - 1" :aria-label="t('retryRules.down')" data-test="retry-move-down" @click="moveRule(draft, rule.id, 1)">{{ t('retryRules.moveDown') }}</button>
                        <button type="button" class="btn btn-secondary btn-sm" :disabled="saving" @click="editing = duplicateRule(rule, t('retryRules.copyName', { name: rule.name }))">{{ t('retryRules.copyRule') }}</button>
                        <button type="button" class="btn btn-ghost btn-sm text-red-600" :disabled="saving" @click="draft.rules = draft.rules.filter(item => item.id !== rule.id)">{{ t('common.delete') }}</button>
                      </div>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </template>
        <p v-else-if="loading" class="p-6 text-sm text-gray-500 dark:text-gray-400" role="status">{{ t('common.loading') }}</p>
      </section>

      <RetrySamples v-else-if="activeTab === 'samples'" :draft="draft" :initial-event-id="initialEventId" @create-rule="addFromSample" />
      <RetryVersions v-else :active="active" :dirty="dirty" :busy="saving || conflict" @rollback="rollback" />
    </div>

    <RetryRuleEditor v-if="editing" :key="editing.id" :rule="editing" :sample-message="sampleMessage" @apply="applyRule" @close="closeEditor" />
    <RetryTextPreview v-if="showTextPreview && draft" :draft="draft" @close="showTextPreview = false" />
    <ConfirmDialog :show="showReload" :title="t('retryRules.reload')" :message="t('retryRules.reloadConfirm')" :confirm-text="t('retryRules.reload')" @confirm="confirmReload" @cancel="showReload = false" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { asyncImageRetryRulesAPI, type RetryConfig, type RetryFact, type RetryRule } from '@/api/admin/asyncImageRetryRules'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAppStore } from '@/stores/app'
import RetryRuleEditor from './RetryRuleEditor.vue'
import RetrySamples from './RetrySamples.vue'
import RetryTextPreview from './RetryTextPreview.vue'
import RetryVersions from './RetryVersions.vue'
import { buildRuleFromError, cloneDraft, duplicateRule, matchingRules, moveRule, newRule } from './draft'

const { t } = useI18n()
const app = useAppStore()
const props = defineProps<{ initialEventId?: number }>()
const tabs = ['rules', 'samples', 'versions'] as const
const activeTab = ref<typeof tabs[number]>('rules')
const initialEventId = computed(() => props.initialEventId && Number.isSafeInteger(props.initialEventId) && props.initialEventId > 0 ? props.initialEventId : undefined)
const active = ref<RetryConfig | null>(null)
const draft = ref<RetryConfig | null>(null)
const editing = ref<RetryRule | null>(null)
const sampleMessage = ref('')
const search = ref('')
const loading = ref(false)
const saving = ref(false)
const conflict = ref(false)
const loadError = ref('')
const saveError = ref('')
const showReload = ref(false)
const showTextPreview = ref(false)
let configController: AbortController | undefined

const dirty = computed(() => JSON.stringify(draft.value) !== JSON.stringify(active.value))
const visibleRules = computed(() => matchingRules(draft.value?.rules || [], search.value))
const indexes = computed(() => new Map(draft.value?.rules.map((rule, index) => [rule.id, index])))

function acceptConfig(config: RetryConfig) {
  active.value = { ...config, rules: config.rules || [] }
  draft.value = cloneDraft(active.value)
  conflict.value = false
  saveError.value = ''
}

async function loadConfig() {
  configController?.abort()
  const controller = new AbortController()
  configController = controller
  loading.value = true
  loadError.value = ''
  try {
    const config = await asyncImageRetryRulesAPI.getConfig(controller.signal)
    if (!controller.signal.aborted) acceptConfig(config)
  } catch {
    if (!controller.signal.aborted) loadError.value = t('retryRules.recoverError')
  } finally {
    if (configController === controller) loading.value = false
  }
}

function resetDraft() {
  if (active.value) draft.value = cloneDraft(active.value)
  saveError.value = ''
}

function confirmReload() {
  showReload.value = false
  void loadConfig()
}

function applyRule(rule: RetryRule) {
  if (!draft.value) return
  const index = draft.value.rules.findIndex(item => item.id === rule.id)
  if (index < 0) draft.value.rules.push(rule)
  else draft.value.rules.splice(index, 1, rule)
  search.value = ''
  closeEditor()
}

function closeEditor() {
  editing.value = null
  sampleMessage.value = ''
}

function addFromSample(fact: RetryFact) {
  if (!draft.value) return
  activeTab.value = 'rules'
  sampleMessage.value = fact.message || ''
  editing.value = buildRuleFromError(fact)
}

async function commit(send: () => Promise<RetryConfig>) {
  saving.value = true
  saveError.value = ''
  try {
    acceptConfig(await send())
    app.showSuccess(t('retryRules.saved'))
  } catch (err) {
    const error = err as { status?: number; message?: string }
    if (error.status === 409) conflict.value = true
    else {
      saveError.value = error.message || t('retryRules.saveError')
      if (activeTab.value !== 'rules') app.showError(saveError.value)
    }
  } finally {
    saving.value = false
  }
}

async function save() {
  if (!draft.value || !dirty.value || saving.value || loading.value || conflict.value) return
  const snapshot = cloneDraft(draft.value)
  await commit(() => asyncImageRetryRulesAPI.save(snapshot))
}

async function rollback(targetRevision: number) {
  if (!active.value || dirty.value || saving.value || loading.value || conflict.value) return
  const revision = active.value.revision
  await commit(() => asyncImageRetryRulesAPI.rollback(revision, targetRevision))
}

async function copyRuleID(id: string) {
  try {
    await navigator.clipboard.writeText(id)
    app.showSuccess(t('retryRules.idCopied'))
  } catch {
    app.showError(t('retryRules.copyFailed'))
  }
}

onMounted(loadConfig)
onUnmounted(() => configController?.abort())
watch(initialEventId, eventID => { if (eventID) activeTab.value = 'samples' }, { immediate: true })
</script>
