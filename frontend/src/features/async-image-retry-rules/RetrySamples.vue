<template>
  <section class="space-y-4">
    <form class="card space-y-4 p-4 sm:p-5" @submit.prevent="search">
      <div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <label><span class="input-label">{{ t('retryRules.startDate') }}</span><input v-model="input.start" type="date" class="input" :max="input.end || undefined" /></label>
        <label><span class="input-label">{{ t('retryRules.endDate') }}</span><input v-model="input.end" type="date" class="input" :min="input.start || undefined" /></label>
        <label v-for="field in textFilters" :key="field.key"><span class="input-label">{{ t(`retryRules.${field.label}`) }}</span><input v-model.trim="input[field.key]" class="input" maxlength="180" /></label>
        <label><span class="input-label">{{ t('retryRules.accountId') }}</span><input v-model="input.account_id" type="number" min="1" :max="Number.MAX_SAFE_INTEGER" class="input" /></label>
        <label><span class="input-label">{{ t('retryRules.stage') }}</span><select v-model="input.stage" class="input"><option value="">–</option><option v-for="stage in retryStages" :key="stage" :value="stage">{{ t(`retryRules.stages.${stage}`) }}</option></select></label>
        <label><span class="input-label">{{ t('retryRules.source') }}</span><select v-model="input.source" class="input"><option value="">–</option><option v-for="source in sources" :key="source" :value="source">{{ t(`retryRules.sources.${source}`) }}</option></select></label>
      </div>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('retryRules.sampleHint') }}</p>
        <button type="submit" class="btn btn-secondary btn-sm">{{ t('retryRules.filter') }}</button>
      </div>
    </form>
    <div class="card overflow-hidden" :aria-busy="loading">
      <div class="flex justify-end gap-2 border-b border-gray-200 p-3 dark:border-dark-700">
        <button v-if="batchRunning" type="button" class="btn btn-secondary btn-sm" @click="cancelBatch">{{ t('retryRules.cancelBatch') }}</button>
        <button v-else type="button" class="btn btn-secondary btn-sm" :disabled="loading || !records.length || !draft" @click="runBatch">{{ t('retryRules.batchPreview') }}</button>
      </div>
      <p v-if="error" class="p-4 text-sm text-red-700 dark:text-red-300" role="alert">{{ error }}</p>
      <p v-else-if="loading || !records.length" class="p-6 text-center text-sm text-gray-500 dark:text-gray-400" role="status">{{ t(loading ? 'common.loading' : 'retryRules.emptySamples') }}</p>
      <div v-else class="overflow-x-auto">
        <table class="w-full text-left text-sm">
          <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800 dark:text-gray-400"><tr><th v-for="column in columns" :key="column" scope="col" class="px-4 py-3">{{ t(`retryRules.${column}`) }}</th></tr></thead>
          <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
            <tr v-for="sample in records" :key="sample.fingerprint">
              <td class="min-w-60 px-4 py-3 align-top">
                <p class="line-clamp-3 break-words font-medium text-gray-900 dark:text-white">{{ sample.latest.fact.message || sample.latest.fact.error_code || '–' }}</p>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ factSummary(sample.latest.fact) }}</p>
              </td>
              <td class="min-w-44 px-4 py-3 align-top"><RetryDecisionView :decision="sample.latest.decision" /></td>
              <td class="px-4 py-3 align-top font-mono">{{ sample.occurrences }}</td>
              <td class="px-4 py-3 align-top font-mono">{{ sample.tasks }}</td>
              <td class="px-4 py-3 align-top font-mono">{{ sample.executions }}</td>
              <td class="px-4 py-3 align-top font-mono">{{ sample.succeeded_tasks }}</td>
              <td class="whitespace-nowrap px-4 py-3 align-top">{{ formatTime(sample.last_seen_at) }}</td>
              <td class="min-w-40 px-4 py-3 align-top">
                <button type="button" class="btn btn-secondary btn-sm" @click="openGroup(sample)">{{ t('retryRules.details') }}</button>
                <div v-if="batchResults[sample.fingerprint]" class="mt-2 text-xs" aria-live="polite">
                  <button v-if="batchResults[sample.fingerprint].preview" type="button" class="btn btn-ghost btn-sm text-primary-700 dark:text-primary-300" @click="showBatchResult(sample)">{{ t('retryRules.previewResult') }}</button>
                  <p v-else class="text-red-700 dark:text-red-300">{{ batchResults[sample.fingerprint].error }}</p>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <Pagination v-if="total" :total="total" :page="page" :page-size="pageSize" @update:page="changePage" @update:page-size="changePageSize" />
    </div>

    <BaseDialog v-if="selectedSample" :show="true" :title="t('retryRules.detailsTitle')" width="extra-wide" @close="closeGroup">
      <p v-if="groupError" class="text-sm text-red-700 dark:text-red-300" role="alert">{{ groupError }}</p>
      <p v-else-if="groupLoading || !groupEvents.length" class="text-sm text-gray-500 dark:text-gray-400" role="status">{{ t(groupLoading ? 'common.loading' : 'retryRules.emptyEvents') }}</p>
      <div v-else class="grid gap-4 lg:grid-cols-[minmax(230px,1fr)_minmax(0,2fr)]">
        <div>
          <ul class="space-y-2">
            <li v-for="event in groupEvents" :key="event.id">
              <button type="button" class="w-full rounded-lg border p-3 text-left text-sm" :class="event.id === selectedEventID ? 'border-primary-400 bg-primary-50 dark:border-primary-800 dark:bg-primary-950/30' : 'border-gray-200 hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-800'" :aria-pressed="event.id === selectedEventID" @click="openEvent(event.id)">
                <p>{{ formatTime(event.fact.occurred_at || event.created_at) }}</p>
                <p class="break-all font-mono text-xs text-gray-500 dark:text-gray-400">{{ event.fact.task_id || '–' }}</p>
              </button>
            </li>
          </ul>
          <Pagination v-if="groupTotal > eventPageSize" :total="groupTotal" :page="groupPage" :page-size="eventPageSize" :show-page-size-selector="false" @update:page="loadGroup" />
        </div>
        <div v-if="selectedEvent" class="min-w-0 space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-700">
          <p class="whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 text-sm dark:bg-dark-800">{{ selectedEvent.fact.message || '–' }}</p>
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ factSummary(selectedEvent.fact) }}</p>
          <dl class="grid gap-3 text-sm sm:grid-cols-2">
            <div v-for="field in eventDetails" :key="field.label"><dt class="font-medium">{{ t(`retryRules.${field.label}`) }}</dt><dd class="break-all text-gray-600 dark:text-gray-300">{{ field.value || '–' }}</dd></div>
          </dl>
          <RetryDecisionView :decision="selectedEvent.decision" />
          <div class="flex flex-wrap gap-2">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="!draft || previewLoading" @click="testEvent">{{ t('retryRules.samplePreview') }}</button>
            <button type="button" class="btn btn-primary btn-sm" :disabled="!draft" @click="emit('createRule', selectedEvent.fact)">{{ t('retryRules.fillFromSample') }}</button>
          </div>
          <p v-if="previewError" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ previewError }}</p>
          <RetryPreviewView v-if="eventPreview" :preview="eventPreview" />
        </div>
        <p v-else-if="detailError" class="text-sm text-red-700 dark:text-red-300" role="alert">{{ detailError }}</p>
        <p v-else class="text-sm text-gray-500 dark:text-gray-400" role="status">{{ t(detailLoading ? 'common.loading' : 'retryRules.selectEvent') }}</p>
      </div>
    </BaseDialog>
    <BaseDialog v-if="batchResult" :show="true" :title="t('retryRules.previewResult')" width="wide" @close="batchResult = null"><RetryPreviewView :preview="batchResult" /></BaseDialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import { asyncImageRetryRulesAPI, retryStages, type RetryConfig, type RetryEvent, type RetryFact, type RetryPreview, type RetrySample, type RetrySampleFilters } from '@/api/admin/asyncImageRetryRules'
import { cloneDraft, previewPage } from './draft'
import RetryDecisionView from './RetryDecisionView.vue'
import RetryPreviewView from './RetryPreviewView.vue'

const props = defineProps<{ draft: RetryConfig | null; initialEventId?: number }>()
const emit = defineEmits<{ createRule: [fact: RetryFact] }>()
const { t, locale } = useI18n()
const sources = ['custom', 'builtin', 'none']
const columns = ['lastError', 'latestDecision', 'occurrences', 'tasks', 'executions', 'successfulTasks', 'lastSeen', 'details']
const textFilters = [{ key: 'platform', label: 'platform' }, { key: 'model', label: 'model' }, { key: 'category', label: 'category' }] as const
const input = reactive({ start: '', end: '', platform: '', model: '', category: '', stage: '', source: '', account_id: '' as number | string })
const filters = ref<RetrySampleFilters>({})
const records = ref<RetrySample[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const loading = ref(false)
const error = ref('')
const selectedSample = ref<{ fingerprint: string } | null>(null)
const groupEvents = ref<RetryEvent[]>([])
const groupPage = ref(1)
const groupTotal = ref(0)
const eventPageSize = 10
const groupError = ref('')
const groupLoading = ref(false)
const selectedEvent = ref<RetryEvent | null>(null)
const selectedEventID = ref<number | null>(null)
const detailLoading = ref(false)
const detailError = ref('')
const eventPreview = ref<RetryPreview | null>(null)
const previewError = ref('')
const previewLoading = ref(false)
const batchRunning = ref(false)
const batchResults = ref<Record<string, { preview?: RetryPreview; error?: string }>>({})
const batchResult = ref<RetryPreview | null>(null)
let listController: AbortController | undefined
let groupController: AbortController | undefined
let eventController: AbortController | undefined
let previewController: AbortController | undefined
let batchController: AbortController | undefined
let deepLinkController: AbortController | undefined

function formatTime(value?: string) {
  if (!value) return '–'
  return new Date(value).toLocaleString(locale.value)
}

const eventDetails = computed(() => {
  const event = selectedEvent.value
  if (!event) return []
  return [
    { label: 'taskId', value: event.fact.task_id }, { label: 'attemptId', value: event.fact.attempt_id },
    { label: 'groupId', value: event.fact.group_id }, { label: 'accountId', value: event.fact.account_id },
    { label: 'targetAccount', value: event.target_account_id }, { label: 'runtimeStatus', value: event.final_status },
    { label: 'occurredAt', value: formatTime(event.fact.occurred_at || event.created_at) },
    { label: 'executionStarted', value: formatTime(event.started_at) },
    { label: 'generationStarted', value: formatTime(event.generation_started_at) },
  ]
})

function factSummary(fact: RetryFact) {
  return [fact.platform, fact.model, fact.stage && t(`retryRules.stages.${fact.stage}`, fact.stage), fact.http_status, fact.error_code, fact.category].filter(Boolean).join(' · ')
}

function search() {
  const asTimestamp = (date: string, nextDay = false) => {
    if (!date) return undefined
    const value = new Date(`${date}T00:00:00`)
    if (nextDay) value.setDate(value.getDate() + 1)
    return value.toISOString()
  }
  filters.value = {
    from: asTimestamp(input.start), to: asTimestamp(input.end, true),
    platform: input.platform || undefined, model: input.model || undefined,
    category: input.category || undefined, stage: input.stage || undefined,
    source: input.source || undefined, account_id: input.account_id ? Number(input.account_id) : undefined,
  }
  changePage(1)
}

function changePage(value: number) { page.value = value; void loadSamples() }
function changePageSize(value: number) { pageSize.value = value; changePage(1) }

async function loadSamples() {
  cancelBatch()
  batchResults.value = {}
  listController?.abort()
  const controller = new AbortController()
  listController = controller
  loading.value = true
  error.value = ''
  try {
    const data = await asyncImageRetryRulesAPI.samples({ ...filters.value, page: page.value, page_size: pageSize.value }, controller.signal)
    if (controller.signal.aborted) return
    records.value = data.items || []
    total.value = data.total
  } catch {
    if (!controller.signal.aborted) error.value = t('retryRules.recoverError')
  } finally {
    if (listController === controller) loading.value = false
  }
}

function clearEvent() {
  eventController?.abort()
  previewController?.abort()
  selectedEvent.value = null
  selectedEventID.value = null
  detailLoading.value = false
  detailError.value = ''
  previewLoading.value = false
  eventPreview.value = null
  previewError.value = ''
}

function openGroup(sample: RetrySample) { selectedSample.value = sample; void loadGroup(1) }

function closeGroup() {
  groupController?.abort()
  clearEvent()
  selectedSample.value = null
}

async function loadGroup(page: number, preferredEvent?: RetryEvent) {
  if (!selectedSample.value) return
  clearEvent()
  groupController?.abort()
  const controller = new AbortController()
  groupController = controller
  groupPage.value = page
  groupLoading.value = true
  groupError.value = ''
  try {
    const data = await asyncImageRetryRulesAPI.sampleEvents(selectedSample.value.fingerprint, { ...filters.value, page, page_size: eventPageSize }, controller.signal)
    if (controller.signal.aborted) return
    groupEvents.value = data.items || []
    groupTotal.value = data.total
    if (preferredEvent) {
      if (!groupEvents.value.some(event => event.id === preferredEvent.id)) groupEvents.value.unshift(preferredEvent)
      selectedEventID.value = preferredEvent.id
      selectedEvent.value = preferredEvent
    } else if (groupEvents.value.length) {
      void openEvent(groupEvents.value[0].id)
    }
  } catch {
    if (!controller.signal.aborted) groupError.value = t('retryRules.sampleUnavailable')
  } finally {
    if (groupController === controller) groupLoading.value = false
  }
}

async function openEvent(id: number) {
  clearEvent()
  const controller = new AbortController()
  eventController = controller
  selectedEventID.value = id
  detailLoading.value = true
  try {
    const event = await asyncImageRetryRulesAPI.event(id, controller.signal)
    if (!controller.signal.aborted) selectedEvent.value = event
  } catch {
    if (!controller.signal.aborted) detailError.value = t('retryRules.sampleUnavailable')
  } finally {
    if (eventController === controller) detailLoading.value = false
  }
}

async function testEvent() {
  if (!props.draft || !selectedEvent.value) return
  previewController?.abort()
  const controller = new AbortController()
  previewController = controller
  previewLoading.value = true
  eventPreview.value = null
  previewError.value = ''
  try {
    const result = await asyncImageRetryRulesAPI.preview({ draft: cloneDraft(props.draft), event_id: selectedEvent.value.id }, controller.signal)
    if (!controller.signal.aborted) eventPreview.value = result
  } catch (err) {
    if (!controller.signal.aborted) previewError.value = (err as { message?: string }).message || t('retryRules.previewError')
  } finally {
    if (previewController === controller) previewLoading.value = false
  }
}

function cancelBatch() {
  batchController?.abort()
  batchController = undefined
  batchRunning.value = false
}

async function runBatch() {
  if (!props.draft || batchRunning.value) return
  const draft = cloneDraft(props.draft)
  const samples = records.value.map(sample => ({ fingerprint: sample.fingerprint, eventID: sample.latest.id }))
  const controller = new AbortController()
  batchController = controller
  batchRunning.value = true
  batchResults.value = {}
  try {
    await previewPage(samples, async ({ fingerprint, eventID }) => {
      try {
        const preview = await asyncImageRetryRulesAPI.preview({ draft, event_id: eventID }, controller.signal)
        if (!controller.signal.aborted) batchResults.value[fingerprint] = { preview }
      } catch (err) {
        if (!controller.signal.aborted) batchResults.value[fingerprint] = { error: (err as { message?: string }).message || t('retryRules.previewError') }
      }
    }, controller.signal)
  } finally {
    if (batchController === controller) batchRunning.value = false
  }
}

function showBatchResult(sample: RetrySample) { batchResult.value = batchResults.value[sample.fingerprint]?.preview ?? null }

async function openLinkedEvent(eventID?: number) {
  deepLinkController?.abort()
  if (!eventID) return
  const controller = new AbortController()
  deepLinkController = controller
  try {
    const event = await asyncImageRetryRulesAPI.event(eventID, controller.signal)
    if (controller.signal.aborted) return
    selectedSample.value = { fingerprint: event.fingerprint }
    await loadGroup(1, event)
  } catch {
    if (!controller.signal.aborted) error.value = t('retryRules.sampleUnavailable')
  }
}

onMounted(loadSamples)
watch(() => props.initialEventId, openLinkedEvent, { immediate: true })
onUnmounted(() => { listController?.abort(); deepLinkController?.abort(); closeGroup(); cancelBatch() })
</script>
