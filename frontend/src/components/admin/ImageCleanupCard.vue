<template>
  <section class="card p-6" aria-labelledby="image-cleanup-title">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div>
        <h3 id="image-cleanup-title" class="text-base font-semibold text-gray-900 dark:text-white">{{ t('imageCleanup.title') }}</h3>
        <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">{{ t('imageCleanup.description') }}</p>
      </div>
      <span class="rounded-full bg-emerald-50 px-3 py-1 text-xs font-medium text-emerald-800 dark:bg-emerald-900/30 dark:text-emerald-300">{{ t('imageCleanup.limited') }}</span>
    </div>
    <p v-if="loadError" role="alert" class="mt-4 text-sm text-red-600">{{ loadError }} <button class="underline" type="button" @click="load">{{ t('imageCleanup.retry') }}</button></p>
    <div class="mt-5 grid gap-6 lg:grid-cols-2">
      <form class="min-w-0" data-testid="cleanup-policy" @submit.prevent="savePolicy">
        <h4 class="mb-3 text-sm font-semibold text-gray-900 dark:text-white">{{ t('imageCleanup.automatic') }}</h4>
        <fieldset :disabled="!loaded || saving" class="space-y-3">
          <label class="flex items-center gap-2 text-sm"><input v-model="policy.enabled" type="checkbox" data-testid="cleanup-enabled" />{{ t('imageCleanup.enabled') }}</label>
          <p v-if="policy.configured === false" class="text-xs text-gray-500 dark:text-gray-400">{{ t('imageCleanup.legacyHint') }}</p>
          <div class="grid grid-cols-2 gap-3">
            <label class="cleanup-label"><span>{{ t('imageCleanup.retention') }}</span><input v-model.number="policy.retention_value" data-testid="retention-value" type="number" min="1" :max="policy.retention_unit === 'days' ? 3650 : 87600" step="1" required class="input w-full" /></label>
            <label class="cleanup-label"><span>{{ t('imageCleanup.unit') }}</span><select v-model="policy.retention_unit" data-testid="retention-unit" class="input w-full"><option value="hours">{{ t('imageCleanup.hours') }}</option><option value="days">{{ t('imageCleanup.days') }}</option></select></label>
          </div>
          <label class="cleanup-label"><span>{{ t('imageCleanup.schedule') }}</span><select v-model="policy.schedule_mode" data-testid="schedule-mode" class="input w-full"><option value="interval">{{ t('imageCleanup.interval') }}</option><option value="daily">{{ t('imageCleanup.daily') }}</option></select></label>
          <label v-if="policy.schedule_mode === 'interval'" class="cleanup-label"><span>{{ t('imageCleanup.intervalMinutes') }}</span><input v-model.number="policy.interval_minutes" data-testid="interval-minutes" type="number" min="1" max="10080" step="1" required class="input w-full" /></label>
          <label v-else class="cleanup-label"><span>{{ t('imageCleanup.dailyTime') }}</span><input v-model="policy.daily_time" data-testid="daily-time" type="time" required class="input w-full" /></label>
          <div class="grid grid-cols-2 gap-3">
            <label class="cleanup-label"><span>{{ t('imageCleanup.batch') }}</span><input v-model.number="policy.batch_size" data-testid="batch-size" type="number" min="1" max="100" step="1" required class="input w-full" /></label>
            <label class="cleanup-label"><span>{{ t('imageCleanup.rate') }}</span><input v-model.number="policy.requests_per_second" data-testid="delete-rate" type="number" min="1" max="5" step="1" required class="input w-full" /></label>
          </div>
          <p class="text-xs leading-relaxed text-gray-500 dark:text-gray-400">{{ t('imageCleanup.resourceHint') }}</p>
          <div class="flex flex-wrap gap-2"><button type="submit" class="btn btn-primary btn-sm" :disabled="!policyValid">{{ saving ? t('common.loading') : t('imageCleanup.savePolicy') }}</button><button type="button" class="btn btn-secondary btn-sm" @click="load">{{ t('imageCleanup.reloadPolicy') }}</button></div>
        </fieldset>
        <p class="mt-3 text-xs text-gray-500 dark:text-gray-400">{{ t('imageCleanup.nextRun', { time: savedNextRun ? formatTime(savedNextRun) : t('imageCleanup.notScheduled') }) }}</p>
      </form>
      <form class="min-w-0 border-t border-gray-200 pt-5 dark:border-gray-700 lg:border-l lg:border-t-0 lg:pl-6 lg:pt-0" data-testid="cleanup-manual" @submit.prevent="previewManual">
        <h4 class="mb-3 text-sm font-semibold text-gray-900 dark:text-white">{{ t('imageCleanup.manual') }}</h4>
        <p class="mb-3 text-xs leading-relaxed text-gray-500 dark:text-gray-400">{{ t('imageCleanup.manualHint') }}</p>
        <fieldset :disabled="busy" class="grid grid-cols-2 gap-3">
          <label class="cleanup-label"><span>{{ t('imageCleanup.olderThan') }}</span><input v-model.number="manual.value" data-testid="manual-value" type="number" min="1" :max="manual.unit === 'days' ? 3650 : 87600" step="1" required class="input w-full" /></label>
          <label class="cleanup-label"><span>{{ t('imageCleanup.unit') }}</span><select v-model="manual.unit" data-testid="manual-unit" class="input w-full"><option value="hours">{{ t('imageCleanup.hours') }}</option><option value="days">{{ t('imageCleanup.days') }}</option></select></label>
          <button type="submit" class="btn btn-secondary btn-sm justify-self-start" data-testid="manual-preview" :disabled="!manualValid">{{ busy ? t('common.loading') : t('imageCleanup.preview') }}</button>
        </fieldset>
        <div v-if="preview" class="mt-4 space-y-2 rounded-lg border border-amber-200 bg-amber-50 p-4 text-sm text-amber-950 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-100" role="status">
          <p class="font-semibold">{{ t('imageCleanup.cutoff', { time: formatTime(preview.before) }) }}</p>
          <p>{{ t('imageCleanup.matches', { count: preview.matched_items, bytes: formatBytes(preview.matched_bytes) }) }}</p>
          <p>{{ t('imageCleanup.protected', { count: preview.protected_count ?? 0 }) }}</p>
          <p class="text-xs leading-relaxed">{{ t('imageCleanup.protectionRecheck') }}</p>
          <p v-if="preview.truncated" class="text-xs">{{ t('imageCleanup.truncated', { count: preview.scanned_count ?? 0 }) }}</p>
          <p class="text-xs leading-relaxed">{{ t('imageCleanup.deleteHint') }}</p>
          <button type="button" class="btn btn-danger btn-sm" data-testid="manual-start" :disabled="busy || !preview.preview_token || !preview.before || (preview.scanned_count ?? preview.matched_items) === 0" @click="startManual">{{ t('imageCleanup.start') }}</button>
        </div>
      </form>
    </div>
    <div class="mt-6 border-t border-gray-200 pt-4 dark:border-gray-700">
      <div class="flex items-center justify-between gap-3"><h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('imageCleanup.history') }}</h4><button type="button" class="btn btn-secondary btn-sm" :disabled="refreshing" @click="refreshJobs()">{{ t('common.refresh') }}</button></div>
      <p v-if="!jobs.length" class="py-4 text-sm text-gray-500">{{ t('common.noData') }}</p>
      <ol v-else class="mt-2 divide-y divide-gray-100 dark:divide-gray-800">
        <li v-for="job in jobs" :key="job.id" class="py-3" :data-testid="`cleanup-job-${job.id}`">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div class="flex flex-wrap items-center gap-2 text-sm"><strong class="font-mono text-xs">#{{ job.id }}</strong><span>{{ sourceLabel(job.source) }}</span><span class="rounded bg-gray-100 px-2 py-0.5 text-xs dark:bg-gray-800">{{ statusLabel(job.status) }}</span></div>
            <div class="flex gap-2"><button v-if="['pending', 'running', 'paused'].includes(job.status)" type="button" class="btn btn-secondary btn-sm" :disabled="actingJob !== null || job.cancel_requested" @click="changeJob(job, 'cancel')">{{ t(job.cancel_requested ? 'imageCleanup.cancelling' : 'imageCleanup.cancel') }}</button><button v-if="['failed', 'cancelled', 'paused'].includes(job.status)" type="button" class="btn btn-secondary btn-sm" :disabled="actingJob !== null || job.cancel_requested && job.status === 'paused'" @click="changeJob(job, 'resume')">{{ t('imageCleanup.resume') }}</button></div>
          </div>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('imageCleanup.cutoff', { time: formatTime(jobCutoff(job)) }) }} · {{ formatTime(job.created_at) }}</p>
          <p class="mt-1 text-xs leading-relaxed text-gray-600 dark:text-gray-300">{{ t('imageCleanup.progress', { scanned: job.scanned_count ?? 0, deleted: job.deleted_count ?? 0, detached: job.detached_count ?? 0, protected: job.protected_count ?? 0, skipped: job.skipped_count ?? 0, errors: job.failed_count ?? 0, bytes: formatBytes(job.deleted_bytes ?? 0) }) }}</p>
          <p v-if="job.error_message" class="mt-1 break-words text-xs text-red-600 dark:text-red-400">{{ job.error_message }}</p>
        </li>
      </ol>
    </div>
    <TotpStepUpDialog :controller="stepUp" />
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getImageCleanupPolicy, updateImageCleanupPolicy, type ImageCleanupPolicy } from '@/api/admin/backup'
import { cancelCleanupJob, createCleanupJob, listCleanupJobs, previewCleanup, resumeCleanupJob } from '@/api/imageLibrary'
import type { ImageCleanupJob, ImageCleanupPreview } from '@/features/image-workflow/types'
import { useAppStore } from '@/stores'
import { isStepUpBlocked, isStepUpCancelled, stepUpBlockReason, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t, locale } = useI18n()
const emit = defineEmits<{ configured: [value: boolean] }>()
const appStore = useAppStore()
const stepUp = useStepUp()
const policy = reactive<ImageCleanupPolicy>({ enabled: false, retention_value: 90, retention_unit: 'days', schedule_mode: 'interval', interval_minutes: 15, daily_time: '03:00', timezone: 'Asia/Shanghai', batch_size: 50, requests_per_second: 2 })
const loaded = ref(false)
const loadError = ref('')
const saving = ref(false)
const busy = ref(false)
const refreshing = ref(false)
const savedNextRun = ref<string | null>(null)
const manual = reactive<{ value: number; unit: 'hours' | 'days' }>({ value: 7, unit: 'days' })
const preview = ref<ImageCleanupPreview | null>(null)
const jobs = ref<ImageCleanupJob[]>([])
const actingJob = ref<string | number | null>(null)
let timer: ReturnType<typeof setTimeout> | undefined
let disposed = false
let pollCount = 0

function integerIn(value: number, min: number, max: number): boolean { return Number.isInteger(value) && value >= min && value <= max }
function validAge(value: number, unit: 'hours' | 'days'): boolean { return integerIn(value, 1, unit === 'days' ? 3650 : 87600) }
const manualValid = computed(() => validAge(manual.value, manual.unit))
const policyValid = computed(() => validAge(policy.retention_value, policy.retention_unit) && integerIn(policy.batch_size, 1, 100) && integerIn(policy.requests_per_second, 1, 5) && (policy.schedule_mode === 'interval' ? integerIn(policy.interval_minutes, 1, 10080) : /^([01]\d|2[0-3]):[0-5]\d$/.test(policy.daily_time)))
watch(() => [manual.value, manual.unit], () => { preview.value = null })
watch(() => policy.schedule_mode, mode => {
  // Both stored schedule fields must remain valid, including the inactive one.
  if (mode === 'daily' && !integerIn(policy.interval_minutes, 1, 10080)) policy.interval_minutes = 15
  if (mode === 'interval' && !/^([01]\d|2[0-3]):[0-5]\d$/.test(policy.daily_time)) policy.daily_time = '03:00'
})

function reportError(error: unknown) {
  if (isStepUpCancelled(error)) return
  if (isStepUpBlocked(error)) {
    appStore.showError(t(stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN' ? 'stepUp.adminApiKeyForbidden' : 'stepUp.notEnabled'))
    return
  }
  appStore.showError(extractApiErrorMessage(error, t('imageCleanup.failed')))
}

async function load() {
  loadError.value = ''
  loaded.value = false
  try {
    const result = await getImageCleanupPolicy()
    if (disposed) return
    Object.assign(policy, result)
    savedNextRun.value = result.next_run_at ?? null
    loaded.value = true
    emit('configured', result.configured === true)
  } catch (error) { loadError.value = extractApiErrorMessage(error, t('imageCleanup.failed')) }
  await refreshJobs()
}

async function savePolicy() {
  if (!loaded.value || saving.value || !policyValid.value) return
  saving.value = true
  try {
    const result = await stepUp.run(() => updateImageCleanupPolicy({ ...policy }))
    Object.assign(policy, result)
    savedNextRun.value = result.next_run_at ?? null
    emit('configured', result.configured === true)
    appStore.showSuccess(t('imageCleanup.saved'))
  } catch (error) { reportError(error) } finally { saving.value = false }
}

async function previewManual() {
  if (busy.value || !manualValid.value) return
  preview.value = null
  busy.value = true
  const requested = { value: manual.value, unit: manual.unit }
  try {
    const result = await previewCleanup({ scope: 'async_results', older_than_value: requested.value, older_than_unit: requested.unit })
    if (manual.value === requested.value && manual.unit === requested.unit) preview.value = result
  } catch (error) { reportError(error) } finally { busy.value = false }
}

async function startManual() {
  const snapshot = preview.value
  if (busy.value || !snapshot?.preview_token || !snapshot.before || !(snapshot.scanned_count ?? snapshot.matched_items)) return
  if (!window.confirm(t('imageCleanup.confirm', { time: formatTime(snapshot.before), count: snapshot.scanned_count ?? snapshot.matched_items }))) return
  busy.value = true
  try {
    await stepUp.run(() => createCleanupJob({ scope: 'async_results', preview_token: snapshot.preview_token, before: snapshot.before }))
    preview.value = null
    await refreshJobs()
    appStore.showSuccess(t('imageCleanup.queued'))
  } catch (error) { preview.value = null; reportError(error) } finally { busy.value = false }
}

async function refreshJobs(silent = false) {
  if (refreshing.value || disposed) return
  refreshing.value = true
  try {
    const result = await listCleanupJobs({ limit: 30, scope: 'async_results' })
    if (!disposed) jobs.value = result.items.filter(job => job.scope === 'async_results')
  } catch (error) { if (!silent) reportError(error) } finally { refreshing.value = false }
}

async function changeJob(job: ImageCleanupJob, action: 'cancel' | 'resume') {
  if (actingJob.value !== null) return
  actingJob.value = job.id
  try {
    await stepUp.run(() => action === 'cancel' ? cancelCleanupJob(job.id) : resumeCleanupJob(job.id))
    await refreshJobs()
  } catch (error) { reportError(error) } finally { actingJob.value = null }
}

function formatTime(value?: string | null): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat(locale?.value?.startsWith('zh') ? 'zh-CN' : 'en-GB', { timeZone: 'Asia/Shanghai', dateStyle: 'short', timeStyle: 'medium', hour12: false }).format(date) + ' (UTC+8)'
}
function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`
  const unit = Math.min(Math.floor(Math.log(value) / Math.log(1024)), 4)
  return `${(value / 1024 ** unit).toFixed(1)} ${['B', 'KiB', 'MiB', 'GiB', 'TiB'][unit]}`
}
function jobCutoff(job: ImageCleanupJob): string | undefined {
  if (job.before) return job.before
  try {
    const filters = typeof job.filters === 'string' ? JSON.parse(job.filters) : job.filters
    return typeof filters?.before === 'string' ? filters.before : undefined
  } catch { return undefined }
}
function statusLabel(status: string): string {
  return ['pending', 'running', 'succeeded', 'failed', 'cancelled', 'paused'].includes(status) ? t(`imageCleanup.status.${status}`) : status
}
function sourceLabel(source?: string): string {
  return ['manual', 'scheduled', 'legacy'].includes(source ?? 'manual') ? t(`imageCleanup.source.${source ?? 'manual'}`) : source ?? ''
}
async function poll() {
  if (disposed) return
  if (document.visibilityState !== 'hidden') {
    await refreshJobs(true)
    pollCount++
    if (pollCount % 6 === 0) {
      try {
        const current = await getImageCleanupPolicy()
        // Refresh the saved schedule without overwriting edits or their CAS revision.
        if (!disposed) savedNextRun.value = current.next_run_at ?? null
      } catch { /* Background refresh should not interrupt work in the form. */ }
    }
  }
  if (!disposed) timer = setTimeout(poll, 10000)
}
onMounted(async () => { await load(); if (!disposed) timer = setTimeout(poll, 10000) })
onBeforeUnmount(() => { disposed = true; if (timer) clearTimeout(timer) })
</script>

<style scoped>
.cleanup-label { display: block; min-width: 0; }
.cleanup-label > span { display: block; margin-bottom: 0.3rem; font-size: 0.75rem; font-weight: 500; color: #4b5563; }
.dark .cleanup-label > span { color: #9ca3af; }
</style>
