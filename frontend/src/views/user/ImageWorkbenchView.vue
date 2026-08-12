<template>
  <AppLayout>
    <div class="workbench">
      <header class="workbench-header">
        <div class="min-w-0">
          <h1>{{ t('imageWorkbench.title') }}</h1>
          <p>{{ t('imageWorkflow.workbench.description') }}</p>
        </div>
        <div class="workbench-header__actions">
          <RouterLink to="/async-image-tasks" class="btn btn-secondary inline-flex items-center gap-2">
            <Icon name="clock" size="sm" />
            {{ t('imageWorkflow.workbench.taskCenter') }}
          </RouterLink>
          <RouterLink to="/image-library" class="btn btn-secondary inline-flex items-center gap-2">
            <Icon name="inbox" size="sm" />
            {{ t('imageWorkflow.library.title') }}
          </RouterLink>
          <button type="button" class="workbench-icon-button" :disabled="loadingKeys || generating" :title="t('common.refresh')" :aria-label="t('common.refresh')" @click="refreshAll">
            <Icon name="refresh" size="sm" :class="loadingKeys && 'animate-spin'" />
          </button>
        </div>
      </header>

      <div class="workbench-grid">
        <aside class="workbench-column workbench-column--config">
          <section class="workbench-panel" aria-labelledby="workbench-key-heading">
            <div class="workbench-panel__heading">
              <div>
                <h2 id="workbench-key-heading">{{ t('imageWorkbench.selectKey') }}</h2>
                <p>{{ t('imageWorkflow.workbench.keyCount', { count: workbenchKeys.length }) }}</p>
              </div>
              <span v-if="capabilityLoading" class="workbench-spinner" aria-hidden="true"></span>
            </div>

            <Select
              v-model="form.apiKeyId"
              :options="keyOptions"
              :disabled="loadingKeys || generating || asyncTask.active"
              :searchable="true"
              :placeholder="t('imageWorkbench.selectKeyPlaceholder')"
              class="w-full"
            >
              <template #option="{ option, selected }">
                <div class="key-option">
                  <div class="min-w-0">
                    <p class="key-option__name">{{ option.label }}</p>
                    <p class="key-option__meta">{{ option.subtitle }}</p>
                  </div>
                  <Icon v-if="selected" name="check" size="sm" class="text-teal-600" />
                </div>
              </template>
            </Select>

            <div v-if="selectedKey" class="key-summary">
              <div class="key-summary__row">
                <span>{{ t('imageWorkflow.workbench.platform') }}</span>
                <strong>{{ platformLabel }}</strong>
              </div>
              <div class="key-summary__row">
                <span>{{ t('imageWorkflow.workbench.group') }}</span>
                <strong>{{ selectedKey.group?.name || '—' }}</strong>
              </div>
              <div class="key-summary__row">
                <span>{{ t('imageWorkbench.apiKeyLabel') }}</span>
                <strong class="font-mono">{{ maskApiKey(selectedKey.key) }}</strong>
              </div>
            </div>

            <div v-if="capabilityError" class="workbench-alert is-error" role="alert">
              <Icon name="exclamationCircle" size="sm" />
              <span>{{ capabilityError }}</span>
            </div>

            <div
              v-else-if="capabilities || selectedAccess?.supported"
              class="execution-mode"
              :class="`is-${isAsyncMode ? 'async' : 'realtime'}${capabilityLoading && !capabilities ? ' is-pending' : ''}`"
              role="status"
            >
              <span class="execution-mode__icon">
                <Icon :name="isAsyncMode ? 'clock' : 'bolt'" size="md" />
              </span>
              <div class="min-w-0">
                <strong>{{ modeLabel }}</strong>
                <p>{{ isAsyncMode ? t('imageWorkflow.mode.asyncHint') : t('imageWorkflow.mode.realtimeHint') }}</p>
              </div>
              <span class="execution-mode__lock" :title="t('imageWorkflow.mode.controlledByGroup')">
                <Icon name="lock" size="xs" />
              </span>
            </div>
          </section>

          <section class="workbench-panel" aria-labelledby="workbench-model-heading">
            <div class="workbench-panel__heading">
              <div>
                <h2 id="workbench-model-heading">{{ t('imageWorkbench.model') }}</h2>
                <p>{{ t('imageWorkflow.workbench.modelFromGroup') }}</p>
              </div>
            </div>
            <label class="field-label" for="image-model">{{ t('imageWorkbench.model') }}</label>
            <select id="image-model" v-model="form.model" class="input w-full" :disabled="!capabilities || generating || asyncTask.active">
              <option v-for="model in modelOptions" :key="model.id" :value="model.id">{{ model.label }}</option>
            </select>
          </section>

          <section v-if="capabilities?.supports_reference_images && maxReferences > 0" class="workbench-panel" aria-labelledby="workbench-reference-heading">
            <div class="workbench-panel__heading">
              <div>
                <h2 id="workbench-reference-heading">{{ t('imageWorkbench.reference') }}</h2>
                <p>{{ t('imageWorkflow.workbench.referenceCount', { count: referenceImages.length, max: maxReferences }) }}</p>
              </div>
            </div>

            <label class="reference-drop" :class="{ 'is-disabled': generating || asyncTask.active || referenceImages.length >= maxReferences }">
              <input
                ref="fileInput"
                type="file"
                class="sr-only"
                accept="image/png,image/jpeg,image/webp"
                multiple
                :disabled="generating || asyncTask.active || referenceImages.length >= maxReferences"
                @change="onReferenceChange"
              />
              <Icon name="upload" size="md" />
              <span>{{ t('imageWorkbench.uploadTitle') }}</span>
              <small>{{ t('imageWorkflow.workbench.referenceLimitHint') }}</small>
            </label>

            <div v-if="referenceImages.length" class="reference-list">
              <div v-for="reference in referenceImages" :key="reference.id" class="reference-item">
                <img :src="reference.previewUrl" :alt="reference.file.name" />
                <span :title="reference.file.name">{{ reference.file.name }}</span>
                <button type="button" :title="t('common.delete')" :aria-label="t('common.delete')" :disabled="generating || asyncTask.active" @click="removeReference(reference.id)">
                  <Icon name="x" size="xs" />
                </button>
              </div>
            </div>
          </section>
        </aside>

        <main class="workbench-column workbench-column--stage">
          <section class="workbench-panel workbench-panel--stage" aria-labelledby="workbench-prompt-heading">
            <div class="workbench-panel__heading">
              <div>
                <h2 id="workbench-prompt-heading">{{ t('imageWorkbench.promptParams') }}</h2>
                <p>{{ t('imageWorkflow.workbench.privateByDefault') }}</p>
              </div>
              <span v-if="capabilities" class="protocol-badge">{{ protocolLabel }}</span>
            </div>

            <label class="field-label" for="image-prompt">{{ t('imageWorkflow.workbench.prompt') }}</label>
            <textarea
              id="image-prompt"
              v-model="form.prompt"
              class="input prompt-input"
              rows="6"
              maxlength="8000"
              :placeholder="t('imageWorkbench.promptPlaceholder')"
              :disabled="generating || asyncTask.active"
              :aria-describedby="promptError ? 'image-prompt-error' : undefined"
            ></textarea>
            <div class="prompt-footer">
              <p v-if="promptError" id="image-prompt-error" class="field-error" role="alert">{{ promptError }}</p>
              <span v-else></span>
              <span>{{ form.prompt.length }} / 8000</span>
            </div>

            <div v-if="capabilities" class="parameter-grid">
              <label v-if="usesResolutionAspect && sizeOptions.length" class="parameter-field">
                <span>{{ t('imageWorkflow.workbench.resolution') }}</span>
                <select v-model="form.size" class="input" :disabled="generating || asyncTask.active">
                  <option v-for="size in sizeOptions" :key="size" :value="size">{{ size }}</option>
                </select>
              </label>
              <label v-else-if="sizeOptions.length" class="parameter-field">
                <span>{{ t('imageWorkbench.size') }}</span>
                <select v-model="form.size" class="input" :disabled="generating || asyncTask.active">
                  <option v-for="size in sizeOptions" :key="size" :value="size">{{ size }}</option>
                </select>
              </label>

              <label v-if="usesResolutionAspect && aspectRatioOptions.length" class="parameter-field">
                <span>{{ t('imageWorkflow.workbench.aspectRatio') }}</span>
                <select v-model="form.aspectRatio" class="input" :disabled="generating || asyncTask.active">
                  <option v-for="ratio in aspectRatioOptions" :key="ratio" :value="ratio">{{ ratio }}</option>
                </select>
              </label>

              <label v-if="!isGemini && qualityOptions.length" class="parameter-field">
                <span>{{ t('imageWorkbench.quality') }}</span>
                <select v-model="form.quality" class="input" :disabled="generating || asyncTask.active">
                  <option v-for="quality in qualityOptions" :key="quality" :value="quality">{{ quality }}</option>
                </select>
              </label>

              <label v-if="!isGemini && (capabilities?.max_images || 1) > 1" class="parameter-field">
                <span>{{ t('imageWorkbench.count') }}</span>
                <select v-model.number="form.count" class="input" :disabled="generating || asyncTask.active">
                  <option v-for="count in countOptions" :key="count" :value="count">{{ count }}</option>
                </select>
              </label>

              <label v-if="!isGemini && formatOptions.length" class="parameter-field">
                <span>{{ t('imageWorkbench.format') }}</span>
                <select v-model="form.format" class="input" :disabled="generating || asyncTask.active">
                  <option v-for="format in formatOptions" :key="format" :value="format">{{ format.toUpperCase() }}</option>
                </select>
              </label>

              <label v-if="showBackground" class="parameter-field">
                <span>{{ t('imageWorkbench.background') }}</span>
                <select v-model="form.background" class="input" :disabled="generating || asyncTask.active">
                  <option v-for="background in backgroundOptions" :key="background" :value="background">{{ background }}</option>
                </select>
              </label>
            </div>

            <div class="generate-row">
              <button type="button" class="btn btn-secondary" :disabled="generating || asyncTask.active" @click="resetForm">
                {{ t('imageWorkbench.reset') }}
              </button>
              <button
                type="button"
                class="generate-button"
                :class="{ 'is-async': isAsyncMode }"
                :disabled="!canGenerate"
                @click="startGenerate"
              >
                <span v-if="generating" class="workbench-spinner is-light" aria-hidden="true"></span>
                <Icon v-else :name="isAsyncMode ? 'clock' : 'bolt'" size="sm" />
                {{ generateButtonLabel }}
              </button>
            </div>
          </section>

          <section v-if="unknownSubmission" class="workbench-panel unknown-submission" role="alert">
            <div>
              <strong>{{ t('imageWorkflow.workbench.submissionUnknown') }}</strong>
              <p>{{ t('imageWorkflow.workbench.submissionUnknownHint') }}</p>
              <code>{{ pendingSubmission?.request.idempotency_key }}</code>
            </div>
            <button type="button" class="btn btn-secondary" :disabled="generating" @click="retryUnknownSubmission">
              {{ t('imageWorkflow.workbench.confirmSameRequest') }}
            </button>
          </section>

          <section v-if="asyncTask.taskId" class="workbench-panel async-runtime" aria-labelledby="async-runtime-heading" aria-live="polite">
            <div class="async-runtime__header">
              <div>
                <p class="async-runtime__eyebrow">{{ t('imageWorkflow.mode.async') }}</p>
                <h2 id="async-runtime-heading">{{ taskStatusLabel }}</h2>
              </div>
              <div class="async-runtime__id">
                <code>{{ asyncTask.taskId }}</code>
                <button type="button" :title="t('common.copy')" :aria-label="t('common.copy')" @click="copyTaskId">
                  <Icon name="copy" size="sm" />
                </button>
              </div>
            </div>
            <div class="task-track" :aria-label="taskStatusLabel">
              <div v-for="stage in taskStages" :key="stage.key" class="task-stage" :class="stage.state">
                <span><Icon :name="stage.state === 'done' ? 'check' : stage.state === 'failed' ? 'x' : 'clock'" size="xs" /></span>
                <strong>{{ stage.label }}</strong>
              </div>
            </div>
            <div class="task-progress" aria-hidden="true"><span :style="{ width: `${asyncTask.progress}%` }"></span></div>
            <p v-if="asyncTask.error" class="workbench-alert is-error" role="alert">
              <Icon name="exclamationCircle" size="sm" />
              {{ asyncTask.error }}
            </p>
            <div class="async-runtime__footer">
              <span>{{ asyncTask.active ? t('imageWorkflow.workbench.continueAddingHint') : t('imageWorkflow.workbench.safeLeave') }}</span>
              <div class="async-runtime__actions">
                <button
                  v-if="asyncTask.active"
                  type="button"
                  class="btn btn-secondary"
                  :disabled="generating"
                  @click="continueAdding"
                >
                  {{ t('imageWorkflow.workbench.continueAdding') }}
                </button>
                <RouterLink to="/async-image-tasks" class="text-link">{{ t('imageWorkflow.workbench.openTaskCenter') }}</RouterLink>
              </div>
            </div>
          </section>

          <section class="workbench-panel result-panel" aria-labelledby="workbench-results-heading" aria-live="polite">
            <div class="workbench-panel__heading">
              <div>
                <h2 id="workbench-results-heading">{{ t('imageWorkbench.resultPreview') }}</h2>
                <p>{{ resultSummary }}</p>
              </div>
            </div>

            <div v-if="generating && !results.length" class="result-empty">
              <span class="workbench-spinner" aria-hidden="true"></span>
              <strong>{{ isAsyncMode ? t('imageWorkflow.workbench.submittingTask') : t('imageWorkflow.workbench.generatingRealtime') }}</strong>
            </div>
            <div v-else-if="!results.length" class="result-empty">
              <Icon name="grid" size="lg" />
              <strong>{{ t('imageWorkbench.emptyPreview') }}</strong>
              <span>{{ t('imageWorkflow.workbench.emptyResultHint') }}</span>
            </div>
            <div v-else class="result-grid">
              <article v-for="result in results" :key="result.id" class="result-item">
                <button type="button" class="result-item__media" @click="openResultLightbox(result.url, form.prompt)">
                  <img :src="resultThumb(result.url)" :alt="form.prompt" loading="lazy" decoding="async" />
                </button>
                <div class="result-item__footer">
                  <span class="archive-state" :class="`is-${result.archiveStatus}`">
                    <span v-if="result.archiveStatus === 'archiving' || result.archiveStatus === 'publishing' || result.archiveStatus === 'syncing'" class="workbench-spinner" aria-hidden="true"></span>
                    <Icon v-else :name="resultStatusIcon(result.archiveStatus)" size="xs" />
                    {{ archiveStatusLabel(result.archiveStatus) }}
                  </span>
                  <button
                    v-if="result.localOnly && (result.archiveStatus === 'local' || result.archiveStatus === 'failed')"
                    type="button"
                    class="text-link"
                    @click="publishLocalResult(result)"
                  >
                    {{ result.archiveStatus === 'failed' ? t('imageWorkflow.workbench.retryPublish') : t('imageWorkflow.workbench.submitReview') }}
                  </button>
                  <button
                    v-else-if="result.localOnly && result.archiveStatus === 'approved_pending_sync'"
                    type="button"
                    class="text-link"
                    @click="syncApprovedResult(result)"
                  >
                    {{ t('imageWorkflow.workbench.syncToPlaza') }}
                  </button>
                  <button type="button" class="result-icon-button" :title="t('imageWorkbench.view')" :aria-label="t('imageWorkbench.view')" @click="openResultLightbox(result.url, form.prompt)">
                    <Icon name="eye" size="sm" />
                  </button>
                </div>
              </article>
            </div>
          </section>
        </main>

        <aside class="workbench-column workbench-column--library">
          <section class="workbench-panel">
            <ImageLibraryPanel ref="libraryPanel" compact @reuse="reuseLibraryItem" />
          </section>
        </aside>
      </div>

      <ImageLightbox :src="lightboxSrc" :alt="lightboxAlt" @close="lightboxSrc = ''" />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import ImageLightbox from '@/components/common/ImageLightbox.vue'
import Icon from '@/components/icons/Icon.vue'
import ImageLibraryPanel from '@/features/image-workflow/ImageLibraryPanel.vue'
import { keysAPI } from '@/api'
import * as imageAPI from '@/api/imageWorkbench'
import { createPlazaSubmissionRequest, listMyPlazaSubmissionRequests, syncPlazaSubmissionRequest } from '@/api/imageLibrary'
import {
  bindPersonalGalleryRequestId,
  getPersonalGalleryItem,
  removePersonalGalleryItem,
  savePersonalGalleryItem,
} from '@/features/image-workflow/personalGalleryStore'
import { deriveWorkbenchAccess, sameCapabilitySnapshot } from '@/features/image-workflow/policy'
import type { ImageLibraryItem, ImageWorkbenchCapabilities } from '@/features/image-workflow/types'
import type { ApiKey } from '@/types'
import { maskApiKey } from '@/utils/maskApiKey'
import { buildOssThumbnailUrl } from '@/utils/ossThumbnail'
import { useAppStore, useAuthStore } from '@/stores'

interface ReferenceImage {
  id: string
  file: File
  previewUrl: string
}

type ArchiveStatus = 'local' | 'publishing' | 'pending_review' | 'approved_pending_sync' | 'syncing' | 'archiving' | 'archived' | 'failed'
interface WorkbenchResult {
  id: string
  url: string
  archiveStatus: ArchiveStatus
  archiveError?: string
  archiveKey: string
  /** Realtime results stay in the browser until the user explicitly publishes. */
  localOnly?: boolean
  submissionRequestId?: string
  taskId?: string
  resultIndex?: number
  libraryItem?: ImageLibraryItem
}

interface AsyncTaskState {
  active: boolean
  taskId: string
  protocol: 'bb' | 'sc'
  status: 'queued' | 'processing' | 'succeeded' | 'failed'
  progress: number
  error: string
}

interface PendingSubmission {
  request: imageAPI.PreparedAsyncSubmission
  keyId: number
  capabilityVersion: string
  capabilities: ImageWorkbenchCapabilities
}

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const appStore = useAppStore()
const authStore = useAuthStore()
const loadingKeys = ref(false)
const capabilityLoading = ref(false)
const capabilityError = ref('')
const generating = ref(false)
const apiKeys = ref<ApiKey[]>([])
const capabilities = ref<ImageWorkbenchCapabilities | null>(null)
const referenceImages = ref<ReferenceImage[]>([])
const results = ref<WorkbenchResult[]>([])
const fileInput = ref<HTMLInputElement | null>(null)
const lightboxSrc = ref('')
const lightboxAlt = ref('')
const libraryPanel = ref<InstanceType<typeof ImageLibraryPanel> | null>(null)
const CAPABILITY_CACHE_TTL_MS = 5 * 60 * 1000
const CAPABILITY_FRESH_MS = 30 * 1000
const PREFETCH_CONCURRENCY = 2
const PREFETCH_LIMIT = 8
const capabilityCache = new Map<number, { data: ImageWorkbenchCapabilities; fetchedAt: number }>()
let capabilityAbort: AbortController | null = null
let capabilityRequestSeq = 0
let prefetchToken = 0
let keysAbort: AbortController | null = null
let ignoreKeyWatch = false
let requestController: AbortController | null = null
let pollTimer: ReturnType<typeof setTimeout> | null = null
const promptError = ref('')
const unknownSubmission = ref(false)
const pendingSubmission = ref<PendingSubmission | null>(null)

const form = reactive({
  apiKeyId: '' as string | number,
  model: '',
  prompt: '',
  size: '',
  aspectRatio: '',
  quality: '',
  count: 1,
  format: '',
  background: '',
})

const asyncTask = reactive<AsyncTaskState>({
  active: false,
  taskId: '',
  protocol: 'bb',
  status: 'queued',
  progress: 0,
  error: '',
})

const workbenchKeys = computed(() => apiKeys.value.filter((key) => deriveWorkbenchAccess(key).supported))
const selectedKey = computed(() => apiKeys.value.find((key) => String(key.id) === String(form.apiKeyId)) || null)
const selectedAccess = computed(() => (selectedKey.value ? deriveWorkbenchAccess(selectedKey.value) : null))
const keyOptions = computed<SelectOption[]>(() => workbenchKeys.value.map((key) => ({
  value: String(key.id),
  label: `${key.name} ${maskApiKey(key.key)}`,
  subtitle: `${key.group?.name || t('imageWorkbench.ungrouped')} · ${key.group?.platform || '—'} · ${deriveWorkbenchAccess(key).mode === 'async' ? t('imageWorkflow.mode.async') : t('imageWorkflow.mode.realtime')}`,
})))
const isGemini = computed(() => (capabilities.value?.platform || selectedAccess.value?.platform) === 'gemini')
const isOpenAI = computed(() => (capabilities.value?.platform || selectedAccess.value?.platform) === 'openai')
const usesResolutionAspect = computed(() => isGemini.value || (isOpenAI.value && (capabilities.value?.aspect_ratios?.length || 0) > 0))
const isAsyncMode = computed(() => {
  if (capabilities.value) return capabilities.value.execution_mode === 'async'
  return selectedAccess.value?.mode === 'async'
})
const modelOptions = computed(() => capabilities.value?.models || [])
const maxReferences = computed(() => Math.max(0, Number(capabilities.value?.max_reference_images || 0)))
const sizeOptions = computed(() => {
  if (!capabilities.value) return []
  return usesResolutionAspect.value ? capabilities.value.resolutions : capabilities.value.sizes
})
const aspectRatioOptions = computed(() => capabilities.value?.aspect_ratios || [])
const qualityOptions = computed(() => capabilities.value?.qualities || [])
const formatOptions = computed(() => capabilities.value?.output_formats || [])
const backgroundOptions = computed(() => capabilities.value?.backgrounds || [])
const countOptions = computed(() => Array.from({ length: Math.max(1, capabilities.value?.max_images || 1) }, (_, index) => index + 1))
const showBackground = computed(() => !isGemini.value && backgroundOptions.value.length > 0)
const platformLabel = computed(() => {
  const platform = capabilities.value?.platform || selectedKey.value?.group?.platform || ''
  return platform === 'openai' ? 'OpenAI' : platform === 'gemini' ? 'Gemini' : platform === 'grok' ? 'Grok' : '—'
})
const modeLabel = computed(() => isAsyncMode.value ? t('imageWorkflow.mode.async') : t('imageWorkflow.mode.realtime'))
const protocolLabel = computed(() => {
  if (!capabilities.value) return ''
  if (capabilities.value.platform === 'gemini') return isAsyncMode.value ? 'Gemini SC' : 'Gemini Native'
  if (capabilities.value.platform === 'openai') return isAsyncMode.value ? 'OpenAI Async' : 'OpenAI Images'
  return 'Grok Images'
})
const canGenerate = computed(() => Boolean(
  selectedKey.value?.key
  && capabilities.value
  && form.model
  && form.prompt.trim()
  && !generating.value
  && !asyncTask.active
  && !capabilityLoading.value,
))
const generateButtonLabel = computed(() => {
  if (generating.value) return isAsyncMode.value ? t('imageWorkflow.workbench.submittingTask') : t('imageWorkbench.generating')
  return isAsyncMode.value ? t('imageWorkflow.workbench.submitAsync') : t('imageWorkflow.workbench.generateRealtime')
})
const taskStatusLabel = computed(() => t(`imageWorkflow.task.${asyncTask.status}`))
const resultSummary = computed(() => results.value.length
  ? t('imageWorkflow.workbench.resultCount', { count: results.value.length })
  : t('imageWorkflow.workbench.noResult'))
const taskStages = computed(() => {
  const failed = asyncTask.status === 'failed'
  const rank = asyncTask.status === 'queued' ? 0 : asyncTask.status === 'processing' ? 1 : asyncTask.status === 'succeeded' ? 2 : 1
  return [
    { key: 'queued', label: t('imageWorkflow.task.queued'), state: rank > 0 ? 'done' : failed ? 'failed' : 'active' },
    { key: 'processing', label: t('imageWorkflow.task.processing'), state: failed ? 'failed' : rank > 1 ? 'done' : rank === 1 ? 'active' : 'idle' },
    { key: 'succeeded', label: t('imageWorkflow.task.succeeded'), state: rank >= 2 ? 'done' : 'idle' },
  ]
})

async function loadKeys() {
  loadingKeys.value = true
  keysAbort?.abort()
  keysAbort = new AbortController()
  const signal = keysAbort.signal
  try {
    const response = await keysAPI.list(1, 100, {
      status: 'active',
      sort_by: 'created_at',
      sort_order: 'desc',
      include_last_used_ip: false,
    }, { signal })
    apiKeys.value = response.items || []
    ignoreKeyWatch = true
    try {
      if (!workbenchKeys.value.some((key) => String(key.id) === String(form.apiKeyId))) {
        form.apiKeyId = workbenchKeys.value[0] ? String(workbenchKeys.value[0].id) : ''
      }
    } finally {
      await nextTick()
      ignoreKeyWatch = false
    }
    prefetchWorkbenchCapabilities(selectedKey.value?.id)
  } catch (cause: any) {
    if (cause?.name === 'CanceledError' || cause?.code === 'ERR_CANCELED' || cause?.name === 'AbortError') return
    appStore.showError(cause?.message || t('imageWorkbench.loadKeysFailed'))
  } finally {
    if (!signal.aborted) loadingKeys.value = false
  }
}

function readCapabilityCache(apiKeyId: number, maxAgeMs = CAPABILITY_CACHE_TTL_MS) {
  const cached = capabilityCache.get(apiKeyId)
  if (!cached) return null
  if (Date.now() - cached.fetchedAt > maxAgeMs) return null
  return cached
}

function writeCapabilityCache(apiKeyId: number, data: ImageWorkbenchCapabilities) {
  capabilityCache.set(apiKeyId, { data, fetchedAt: Date.now() })
}

function isAbortError(cause: unknown) {
  const error = cause as { name?: string; code?: string }
  return error?.name === 'CanceledError' || error?.code === 'ERR_CANCELED' || error?.name === 'AbortError'
}

async function loadCapabilities(key: ApiKey, quiet = false, options?: { force?: boolean }) {
  const requestSeq = quiet ? capabilityRequestSeq : ++capabilityRequestSeq
  if (!quiet) {
    capabilityAbort?.abort()
    capabilityAbort = new AbortController()
    capabilityLoading.value = true
    capabilityError.value = ''
  }

  const cached = options?.force ? null : readCapabilityCache(key.id)
  if (cached) {
    if (!quiet && requestSeq !== capabilityRequestSeq) return cached.data
    capabilities.value = cached.data
    applyCapabilityDefaults(cached.data)
    if (!quiet) capabilityLoading.value = false
    return cached.data
  }

  try {
    const next = await imageAPI.getCapabilities(key.id, key, quiet ? undefined : capabilityAbort?.signal)
    writeCapabilityCache(key.id, next)
    if (!quiet && requestSeq !== capabilityRequestSeq) return next
    capabilities.value = next
    applyCapabilityDefaults(next)
    return next
  } catch (cause: any) {
    if (isAbortError(cause)) return capabilities.value
    if (!quiet && requestSeq === capabilityRequestSeq) {
      capabilities.value = null
      capabilityError.value = cause?.message || t('imageWorkflow.workbench.capabilityFailed')
    }
    throw cause
  } finally {
    if (!quiet && requestSeq === capabilityRequestSeq) capabilityLoading.value = false
  }
}

function prefetchWorkbenchCapabilities(preferredId?: number) {
  const token = ++prefetchToken
  const targets = workbenchKeys.value
    .filter((key) => key.id !== preferredId && !readCapabilityCache(key.id))
    .slice(0, PREFETCH_LIMIT)
  if (!targets.length) return

  void (async () => {
    let cursor = 0
    const workers = Array.from({ length: Math.min(PREFETCH_CONCURRENCY, targets.length) }, async () => {
      while (token === prefetchToken && cursor < targets.length) {
        const key = targets[cursor++]
        if (!key || readCapabilityCache(key.id)) continue
        try {
          const next = await imageAPI.getCapabilities(key.id, key)
          if (token !== prefetchToken) return
          writeCapabilityCache(key.id, next)
        } catch {
          // Prefetch is best-effort; switch will fetch on demand.
        }
      }
    })
    await Promise.all(workers)
  })()
}

function usesResolutionAspectFor(caps: ImageWorkbenchCapabilities | null | undefined): boolean {
  if (!caps) return false
  if (caps.platform === 'gemini') return true
  return caps.platform === 'openai' && (caps.aspect_ratios?.length || 0) > 0
}

function applyCapabilityDefaults(next: ImageWorkbenchCapabilities) {
  const modelIDs = next.models.map((model) => model.id)
  if (!modelIDs.includes(form.model)) form.model = modelIDs[0] || ''
  const sizes = usesResolutionAspectFor(next) ? next.resolutions : next.sizes
  if (!sizes.includes(form.size)) form.size = sizes[0] || ''
  if (!next.aspect_ratios.includes(form.aspectRatio)) form.aspectRatio = next.aspect_ratios[0] || ''
  if (!next.qualities.includes(form.quality)) form.quality = next.qualities[0] || ''
  if (!next.output_formats.includes(form.format)) form.format = next.output_formats[0] || ''
  if (!next.backgrounds.includes(form.background)) form.background = next.backgrounds[0] || ''
  form.count = Math.min(Math.max(1, form.count), Math.max(1, next.max_images || 1))
  if (!next.supports_reference_images) clearReferences()
}

async function refreshAll() {
  capabilityCache.clear()
  await loadKeys()
  if (selectedKey.value) await loadCapabilities(selectedKey.value, false, { force: true }).catch(() => undefined)
  await libraryPanel.value?.refresh()
}

function resetForm() {
  form.prompt = ''
  promptError.value = ''
  if (capabilities.value) applyCapabilityDefaults(capabilities.value)
  clearReferences()
}

async function onReferenceChange(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files || [])
  input.value = ''
  const room = maxReferences.value - referenceImages.value.length
  for (const file of files.slice(0, room)) {
    if (!['image/png', 'image/jpeg', 'image/webp'].includes(file.type) || file.size > 20 * 1024 * 1024) {
      appStore.showError(t('imageWorkflow.workbench.invalidReference'))
      continue
    }
    referenceImages.value.push({ id: randomID('ref'), file, previewUrl: URL.createObjectURL(file) })
  }
  if (files.length > room) appStore.showError(t('imageWorkbench.referenceLimit', { n: maxReferences.value }))
}

function removeReference(id: string) {
  const target = referenceImages.value.find((item) => item.id === id)
  if (target) URL.revokeObjectURL(target.previewUrl)
  referenceImages.value = referenceImages.value.filter((item) => item.id !== id)
}

function clearReferences() {
  referenceImages.value.forEach((item) => URL.revokeObjectURL(item.previewUrl))
  referenceImages.value = []
  if (fileInput.value) fileInput.value.value = ''
}

async function startGenerate() {
  const key = selectedKey.value
  const snapshot = capabilities.value
  promptError.value = ''
  if (!key || !snapshot) return
  if (!form.prompt.trim()) {
    promptError.value = t('imageWorkflow.workbench.promptRequired')
    return
  }

  generating.value = true
  requestController?.abort()
  requestController = new AbortController()
  try {
    const cachedFresh = readCapabilityCache(key.id, CAPABILITY_FRESH_MS)
    const fresh = cachedFresh?.data || await loadCapabilities(key, true, { force: true })
    if (!fresh) return
    if (!sameCapabilitySnapshot(snapshot, fresh)) {
      capabilities.value = fresh
      applyCapabilityDefaults(fresh)
      appStore.showError(t('imageWorkflow.workbench.capabilityChanged'))
      return
    }

    results.value = []
    if (fresh.execution_mode === 'async') await submitAsync(key, fresh)
    else await runRealtime(key, fresh)
  } catch (cause: any) {
    if (isAbortError(cause) || cause?.name === 'AbortError') return
    appStore.showError(cause?.message || t('imageWorkbench.generateFailed'))
  } finally {
    generating.value = false
  }
}

async function runRealtime(key: ApiKey, current: ImageWorkbenchCapabilities) {
  let response: imageAPI.ImageGenerateResponse
  if (current.platform === 'gemini') {
    const references = await Promise.all(referenceImages.value.map(async ({ file }) => ({
      mimeType: file.type,
      base64: await fileToBase64(file),
    })))
    response = await imageAPI.generateGeminiImage(key.key, {
      model: form.model,
      prompt: form.prompt.trim(),
      resolution: selectedCapabilityOption(sizeOptions.value, form.size),
      aspect_ratio: selectedCapabilityOption(aspectRatioOptions.value, form.aspectRatio),
      references,
    }, requestController?.signal)
  } else if (referenceImages.value.length) {
    response = await imageAPI.editImage(key.key, {
      model: form.model,
      prompt: form.prompt.trim(),
      imageFiles: referenceImages.value.map((item) => item.file),
      n: form.count,
      ...(usesResolutionAspectFor(current)
        ? {
            resolution: selectedCapabilityOption(sizeOptions.value, form.size),
            aspect_ratio: selectedCapabilityOption(aspectRatioOptions.value, form.aspectRatio),
          }
        : { size: selectedCapabilityOption(sizeOptions.value, form.size) }),
      quality: selectedCapabilityOption(qualityOptions.value, form.quality),
      response_format: 'b64_json',
      output_format: selectedCapabilityOption(formatOptions.value, form.format),
      background: showBackground.value ? selectedCapabilityOption(backgroundOptions.value, form.background) : undefined,
    }, requestController?.signal)
  } else {
    response = await imageAPI.generateImage(key.key, {
      model: form.model,
      prompt: form.prompt.trim(),
      n: form.count,
      ...(usesResolutionAspectFor(current)
        ? {
            resolution: selectedCapabilityOption(sizeOptions.value, form.size),
            aspect_ratio: selectedCapabilityOption(aspectRatioOptions.value, form.aspectRatio),
          }
        : { size: selectedCapabilityOption(sizeOptions.value, form.size) }),
      quality: selectedCapabilityOption(qualityOptions.value, form.quality),
      response_format: 'b64_json',
      output_format: selectedCapabilityOption(formatOptions.value, form.format),
      background: showBackground.value ? selectedCapabilityOption(backgroundOptions.value, form.background) : undefined,
    }, requestController?.signal)
  }

  if (!response.data?.length) throw new Error(t('imageWorkbench.emptyResult'))
  const operationKey = imageAPI.createImageIdempotencyKey()
  // Realtime images stay in the browser gallery; durable upload starts after publication approval.
  const nextResults: WorkbenchResult[] = []
  let gallerySaveFailed = false
  for (const [index, item] of response.data.entries()) {
    const url = imageAPI.resultToDataUrl(item, item.output_format || form.format || 'png')
    if (!url) continue
    const archiveKey = `${operationKey}-${index}`
    const result: WorkbenchResult = {
      id: randomID('result'),
      url,
      archiveStatus: 'local',
      archiveKey,
      localOnly: true,
    }
    if (url.startsWith('data:')) {
      const file = dataURLToFile(url, `result.${form.format || 'png'}`)
      try {
        await savePersonalGalleryItem({
          id: archiveKey,
          userId: Number(authStore.user?.id || 0),
          title: truncateTitle(form.prompt) || t('imageWorkflow.library.untitled'),
          file,
          fileName: file.name,
          contentType: file.type || 'image/png',
          platform: capabilities.value?.platform,
          model: form.model,
          prompt: form.prompt.trim(),
          requestedSize: selectedCapabilityOption(sizeOptions.value, form.size),
          aspectRatio: usesResolutionAspect.value ? selectedCapabilityOption(aspectRatioOptions.value, form.aspectRatio) : undefined,
          quality: isGemini.value ? undefined : selectedCapabilityOption(qualityOptions.value, form.quality),
          outputFormat: selectedCapabilityOption(formatOptions.value, form.format),
          apiKeyId: selectedKey.value?.id,
          groupId: selectedKey.value?.group_id,
          metadata: {
            api_key_id: selectedKey.value?.id,
            group_id: selectedKey.value?.group_id,
            platform: capabilities.value?.platform,
            model: form.model,
            prompt: form.prompt.trim(),
          },
        })
      } catch {
        gallerySaveFailed = true
      }
    }
    nextResults.push(result)
  }
  results.value = nextResults
  void libraryPanel.value?.refresh()
  if (gallerySaveFailed) appStore.showWarning(t('imageWorkflow.workbench.gallerySaveFailed'))
  else appStore.showSuccess(t('imageWorkbench.generateSuccess'))
}

async function submitAsync(key: ApiKey, current: ImageWorkbenchCapabilities) {
  const idempotencyKey = imageAPI.createImageIdempotencyKey()
  let prepared: imageAPI.PreparedAsyncSubmission
  if (current.platform === 'gemini') {
    const uploadedURLs: string[] = []
    for (const reference of referenceImages.value) {
      uploadedURLs.push(await imageAPI.uploadGeminiReference(key.key, reference.file, requestController?.signal))
    }
    prepared = imageAPI.prepareGeminiAsyncSubmission(key.key, {
      model: form.model,
      prompt: form.prompt.trim(),
      resolution: selectedCapabilityOption(sizeOptions.value, form.size),
      aspect_ratio: selectedCapabilityOption(aspectRatioOptions.value, form.aspectRatio),
      image_urls: uploadedURLs,
    }, idempotencyKey)
  } else {
    prepared = await imageAPI.prepareOpenAIAsyncSubmission(key.key, {
      model: form.model,
      prompt: form.prompt.trim(),
      imageFiles: referenceImages.value.map((item) => item.file),
      n: form.count,
      ...(usesResolutionAspectFor(current)
        ? {
            resolution: selectedCapabilityOption(sizeOptions.value, form.size),
            aspect_ratio: selectedCapabilityOption(aspectRatioOptions.value, form.aspectRatio),
          }
        : { size: selectedCapabilityOption(sizeOptions.value, form.size) }),
      quality: selectedCapabilityOption(qualityOptions.value, form.quality),
      output_format: selectedCapabilityOption(formatOptions.value, form.format),
      background: showBackground.value ? selectedCapabilityOption(backgroundOptions.value, form.background) : undefined,
    }, idempotencyKey)
  }

  pendingSubmission.value = {
    request: prepared,
    keyId: key.id,
    capabilityVersion: current.capability_version,
    capabilities: current,
  }
  let submission: imageAPI.AsyncImageSubmission
  try {
    submission = await prepared.send(requestController?.signal)
  } catch (cause: any) {
    const status = Number(cause?.status || 0)
    if (!status || status === 408 || status >= 500) unknownSubmission.value = true
    else pendingSubmission.value = null
    throw cause
  }

  unknownSubmission.value = false
  pendingSubmission.value = null
  beginAsyncTask(submission, key, current)
}

function beginAsyncTask(submission: imageAPI.AsyncImageSubmission, key: ApiKey, current: ImageWorkbenchCapabilities) {
  Object.assign(asyncTask, {
    active: true,
    taskId: submission.task_id,
    protocol: submission.protocol,
    status: 'queued',
    progress: 0,
    error: '',
  })
  appStore.showSuccess(t('imageWorkflow.workbench.taskSubmitted'))
  schedulePoll(key, current)
}

/** Unlock compose controls after an async submit so another task can be prepared. */
function continueAdding() {
  if (!asyncTask.active) return
  form.prompt = ''
  promptError.value = ''
  clearReferences()
  asyncTask.active = false
}

async function retryUnknownSubmission() {
  const pending = pendingSubmission.value
  const key = selectedKey.value
  if (!pending || !key || key.id !== pending.keyId || capabilities.value?.capability_version !== pending.capabilityVersion) {
    appStore.showError(t('imageWorkflow.workbench.retryContextChanged'))
    return
  }
  generating.value = true
  try {
    const submission = await pending.request.send()
    unknownSubmission.value = false
    pendingSubmission.value = null
    beginAsyncTask(submission, key, pending.capabilities)
  } catch (cause: any) {
    const status = Number(cause?.status || 0)
    if (status && status !== 408 && status < 500) {
      unknownSubmission.value = false
      pendingSubmission.value = null
    }
    appStore.showError(cause?.message || t('imageWorkflow.workbench.pollFailed'))
  } finally {
    generating.value = false
  }
}

function schedulePoll(key: ApiKey, current: ImageWorkbenchCapabilities, delay = 1200) {
  clearPollTimer()
  pollTimer = setTimeout(() => void pollTask(key, current), delay)
}

async function pollTask(key: ApiKey, current: ImageWorkbenchCapabilities) {
  // Keep polling after "继续添加" unlocks the form (active=false) while the task is in flight.
  if (!asyncTask.taskId || asyncTask.status === 'succeeded' || asyncTask.status === 'failed') return
  const trackedTaskId = asyncTask.taskId
  try {
    const state = await imageAPI.pollAsyncImage(key.key, trackedTaskId, asyncTask.protocol)
    if (asyncTask.taskId !== trackedTaskId) return
    asyncTask.status = state.status
    asyncTask.progress = Math.max(0, Math.min(100, state.progress))
    if (state.status === 'succeeded') {
      asyncTask.active = false
      asyncTask.progress = 100
      const operationKey = imageAPI.createImageIdempotencyKey()
      // Async results are session preview only — never written to the personal gallery.
      results.value = state.images.map((image, index) => ({
        id: randomID('result'),
        url: image.url,
        archiveStatus: 'local',
        archiveKey: `${operationKey}-${index}`,
        taskId: asyncTask.taskId,
        resultIndex: index,
        localOnly: false,
      }))
      appStore.showSuccess(t('imageWorkflow.workbench.taskCompleted'))
      return
    }
    if (state.status === 'failed') {
      asyncTask.active = false
      asyncTask.error = state.fail_reason || t('imageWorkflow.workbench.taskFailed')
      return
    }
    schedulePoll(key, current, 2500)
  } catch (cause: any) {
    if (asyncTask.taskId !== trackedTaskId) return
    asyncTask.error = cause?.message || t('imageWorkflow.workbench.pollFailed')
    // A query failure never changes execution mode or resubmits the model request.
    schedulePoll(key, current, 5000)
  }
}

async function publishLocalResult(result: WorkbenchResult) {
  if (!result.localOnly || !capabilities.value || !selectedKey.value) return
  if (!result.url.startsWith('data:') && !(await getPersonalGalleryItem(result.archiveKey))) {
    appStore.showError(t('imageWorkflow.workbench.localResultUnavailable'))
    return
  }
  if (!window.confirm(t('imageWorkflow.workbench.publishConfirm'))) return
  const sharePrompt = Boolean(form.prompt.trim()) && window.confirm(t('imageWorkflow.library.sharePromptConfirm'))
  result.archiveStatus = 'publishing'
  result.archiveError = ''
  try {
    let blob = await getPersonalGalleryItem(result.archiveKey)
    if (!blob && result.url.startsWith('data:')) {
      const file = dataURLToFile(result.url, `result.${form.format || 'png'}`)
      blob = await savePersonalGalleryItem({
        id: result.archiveKey,
        userId: Number(authStore.user?.id || 0),
        title: truncateTitle(form.prompt) || t('imageWorkflow.library.untitled'),
        file,
        fileName: file.name,
        contentType: file.type || 'image/png',
        platform: capabilities.value.platform,
        model: form.model,
        prompt: form.prompt.trim(),
        apiKeyId: selectedKey.value.id,
        groupId: selectedKey.value.group_id,
      })
    }
    if (!blob) throw new Error(t('imageWorkflow.workbench.localResultUnavailable'))
    const item = await createPlazaSubmissionRequest({
      title: truncateTitle(form.prompt),
      private_prompt: form.prompt.trim(),
      public_title: truncateTitle(form.prompt),
      share_prompt: sharePrompt,
      public_prompt: sharePrompt ? form.prompt.trim() || undefined : undefined,
      api_key_id: selectedKey.value.id,
      group_id: selectedKey.value.group_id,
      platform: capabilities.value.platform,
      generation_mode: 'realtime',
      source_type: 'realtime_import',
      model: form.model,
      requested_size: selectedCapabilityOption(sizeOptions.value, form.size),
      aspect_ratio: usesResolutionAspect.value ? selectedCapabilityOption(aspectRatioOptions.value, form.aspectRatio) : undefined,
      quality: isGemini.value ? undefined : selectedCapabilityOption(qualityOptions.value, form.quality),
      content_type: blob.contentType,
      byte_size: blob.byteSize,
      checksum_sha256: blob.checksumSha256,
      client_blob_key: result.archiveKey,
    }, result.archiveKey)
    result.submissionRequestId = item.id
    result.archiveStatus = item.status === 'approved_pending_sync' ? 'approved_pending_sync' : 'pending_review'
    appStore.showSuccess(t('imageWorkflow.library.submitted'))
    void bindPersonalGalleryRequestId(result.archiveKey, item.id).catch(() => undefined)
    void libraryPanel.value?.refresh()
  } catch (cause: any) {
    result.archiveStatus = 'failed'
    const message = cause?.message || t('imageWorkflow.workbench.publishFailed')
    result.archiveError = message
    appStore.showError(message)
  }
}

async function syncApprovedResult(result: WorkbenchResult) {
  if (!result.submissionRequestId) {
    appStore.showError(t('imageWorkflow.workbench.syncUnavailable'))
    return
  }
  if (!window.confirm(t('imageWorkflow.workbench.syncConfirm'))) return
  result.archiveStatus = 'syncing'
  result.archiveError = ''
  try {
    const blob = await getPersonalGalleryItem(result.archiveKey)
    if (!blob?.file) throw new Error(t('imageWorkflow.workbench.localResultUnavailable'))
    const file = blob.file instanceof File
      ? blob.file
      : new File([blob.file], blob.fileName || 'sync-image.png', { type: blob.contentType || blob.file.type })
    const synced = await syncPlazaSubmissionRequest(result.submissionRequestId, file, file.name)
    result.libraryItem = synced.library_item
    result.archiveStatus = 'archived'
    result.localOnly = false
    await removePersonalGalleryItem(result.archiveKey).catch(() => undefined)
    appStore.showSuccess(t('imageWorkflow.workbench.syncSuccess'))
    await libraryPanel.value?.refresh()
  } catch (cause: any) {
    result.archiveStatus = 'approved_pending_sync'
    const message = cause?.message || t('imageWorkflow.workbench.syncFailed')
    result.archiveError = message
    appStore.showError(message)
  }
}

function archiveStatusLabel(status: ArchiveStatus) {
  return t(`imageWorkflow.archive.${status}`)
}

function resultStatusIcon(status: ArchiveStatus) {
  if (status === 'local') return 'inbox'
  if (status === 'approved_pending_sync') return 'checkCircle'
  if (status === 'archived' || status === 'pending_review') return 'checkCircle'
  return 'exclamationCircle'
}

function reuseLibraryItem(item: ImageLibraryItem) {
  form.prompt = item.prompt || ''
  if (modelOptions.value.some((model) => model.id === item.model)) form.model = item.model
  if (item.requested_size && sizeOptions.value.includes(item.requested_size)) form.size = item.requested_size
  if (item.aspect_ratio && aspectRatioOptions.value.includes(item.aspect_ratio)) form.aspectRatio = item.aspect_ratio
  window.scrollTo({ top: 0, behavior: 'smooth' })
}

function resultThumb(url: string) {
  return buildOssThumbnailUrl(url, { width: 480 })
}

function openResultLightbox(url: string, alt = '') {
  if (!url) return
  lightboxSrc.value = url
  lightboxAlt.value = alt
}

async function copyTaskId() {
  try {
    await navigator.clipboard.writeText(asyncTask.taskId)
    appStore.showSuccess(t('common.copied'))
  } catch {
    appStore.showError(t('common.copyFailed'))
  }
}

function clearPollTimer() {
  if (pollTimer) clearTimeout(pollTimer)
  pollTimer = null
}

function randomID(prefix: string) {
  return `${prefix}_${globalThis.crypto?.randomUUID?.() || `${Date.now()}_${Math.random().toString(36).slice(2)}`}`
}

function selectedCapabilityOption(options: string[], value: string): string | undefined {
  return options.includes(value) ? value : undefined
}

function truncateTitle(value: string) {
  const normalized = value.trim().replace(/\s+/g, ' ')
  return normalized.length > 80 ? `${normalized.slice(0, 79)}…` : normalized
}

function fileToBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || '').split(',')[1] || '')
    reader.onerror = () => reject(reader.error || new Error('Failed to read image'))
    reader.readAsDataURL(file)
  })
}

function dataURLToFile(dataURL: string, filename: string): File {
  const [header, payload] = dataURL.split(',', 2)
  const mime = /data:([^;]+)/.exec(header)?.[1] || 'image/png'
  const bytes = Uint8Array.from(atob(payload || ''), (char) => char.charCodeAt(0))
  return new File([bytes], filename, { type: mime })
}

watch(() => form.apiKeyId, async () => {
  if (ignoreKeyWatch) return
  clearPollTimer()
  asyncTask.active = false
  asyncTask.taskId = ''
  results.value = []
  unknownSubmission.value = false
  pendingSubmission.value = null
  clearReferences()
  if (!selectedKey.value) {
    capabilities.value = null
    capabilityError.value = ''
    capabilityLoading.value = false
    return
  }
  const cached = readCapabilityCache(selectedKey.value.id)
  if (cached) {
    capabilities.value = cached.data
    applyCapabilityDefaults(cached.data)
    capabilityError.value = ''
    capabilityLoading.value = false
    // Refresh in background so UI stays instant on re-select / prefetch hits.
    void loadCapabilities(selectedKey.value, true, { force: true }).catch(() => undefined)
    return
  }
  capabilities.value = null
  await loadCapabilities(selectedKey.value).catch(() => undefined)
})

watch(aspectRatioOptions, (options) => {
  if (usesResolutionAspect.value && !options.includes(form.aspectRatio)) form.aspectRatio = options[0] || '1:1'
})

onMounted(async () => {
  await loadKeys()
  if (selectedKey.value) await loadCapabilities(selectedKey.value).catch(() => undefined)
  const reusePayload = (() => {
    try {
      const raw = sessionStorage.getItem('image-workbench:reuse-payload')
      if (!raw) return null
      sessionStorage.removeItem('image-workbench:reuse-payload')
      return JSON.parse(raw) as { prompt?: string; model?: string; size?: string }
    } catch {
      return null
    }
  })()
  const incomingPrompt = typeof reusePayload?.prompt === 'string'
    ? reusePayload.prompt
    : (typeof route.query.prompt === 'string' ? route.query.prompt : '')
  if (incomingPrompt) form.prompt = incomingPrompt
  const incomingModel = typeof reusePayload?.model === 'string' ? reusePayload.model : route.query.model
  if (typeof incomingModel === 'string' && modelOptions.value.some((model) => model.id === incomingModel)) {
    form.model = incomingModel
  }
  const incomingSize = typeof reusePayload?.size === 'string' ? reusePayload.size : route.query.size
  if (typeof incomingSize === 'string' && sizeOptions.value.includes(incomingSize)) {
    form.size = incomingSize
  }
  if (Object.keys(route.query).length) await nextTick(() => router.replace({ path: route.path }))
  await restoreDeferredSubmissions()
})

onUnmounted(() => {
  keysAbort?.abort()
  capabilityAbort?.abort()
  requestController?.abort()
  prefetchToken += 1
  clearPollTimer()
  clearReferences()
  results.value.forEach((item) => {
    if (item.url.startsWith('blob:')) URL.revokeObjectURL(item.url)
  })
})

async function restoreDeferredSubmissions() {
  const userId = Number(authStore.user?.id || 0)
  if (userId <= 0) return
  try {
    const [pending, approved] = await Promise.all([
      listMyPlazaSubmissionRequests({ status: 'pending_review', limit: 50 }),
      listMyPlazaSubmissionRequests({ status: 'approved_pending_sync', limit: 50 }),
    ])
    const requests = [...approved.items, ...pending.items]
    if (!requests.length) return
    const known = new Map(results.value.map((item) => [item.archiveKey, item]))
    const restored: WorkbenchResult[] = []
    for (const req of requests) {
      const status: ArchiveStatus = req.status === 'approved_pending_sync' ? 'approved_pending_sync' : 'pending_review'
      const existing = known.get(req.client_blob_key)
      if (existing) {
        existing.submissionRequestId = req.id
        existing.archiveStatus = status
        existing.localOnly = true
        continue
      }
      const blob = await getPersonalGalleryItem(req.client_blob_key)
      const url = blob?.file ? URL.createObjectURL(blob.file) : ''
      if (!url) continue
      restored.push({
        id: randomID('result'),
        url,
        archiveStatus: status,
        archiveKey: req.client_blob_key,
        localOnly: true,
        submissionRequestId: req.id,
      })
    }
    if (restored.length) results.value = [...restored, ...results.value]
  } catch {
    // Local restore is best-effort; network/API failures should not block the workbench.
  }
}
</script>

<style scoped>
.workbench { max-width: 1720px; margin: 0 auto; color: #111827; }
.dark .workbench { color: #f3f4f6; }
.workbench-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; margin-bottom: 1rem; }
.workbench-header h1 { font-size: 1.5rem; font-weight: 750; line-height: 1.25; }
.workbench-header p { margin-top: 0.3rem; color: #6b7280; font-size: 0.875rem; }
.dark .workbench-header p { color: #9ca3af; }
.workbench-header__actions { display: flex; flex-wrap: wrap; align-items: center; justify-content: flex-end; gap: 0.5rem; }
.workbench-icon-button,
.result-icon-button { display: inline-grid; width: 2.25rem; height: 2.25rem; place-items: center; border: 1px solid #d1d5db; border-radius: 6px; color: #4b5563; }
.dark .workbench-icon-button,
.dark .result-icon-button { border-color: #374151; color: #d1d5db; }
.workbench-icon-button:hover,
.result-icon-button:hover { border-color: #0d9488; color: #0f766e; }

.workbench-grid { display: grid; grid-template-columns: minmax(230px, 0.78fr) minmax(420px, 1.55fr) minmax(250px, 0.9fr); gap: 0.875rem; align-items: start; }
.workbench-column { min-width: 0; display: flex; flex-direction: column; gap: 0.875rem; }
.workbench-panel { min-width: 0; padding: 0.9rem; border: 1px solid #e5e7eb; border-radius: 8px; background: #fff; }
.dark .workbench-panel { border-color: #374151; background: #111827; }
.workbench-panel__heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 0.75rem; margin-bottom: 0.75rem; }
.workbench-panel__heading h2 { font-size: 0.875rem; font-weight: 750; line-height: 1.4; }
.workbench-panel__heading p { margin-top: 0.15rem; color: #6b7280; font-size: 0.72rem; line-height: 1.4; }
.dark .workbench-panel__heading p { color: #9ca3af; }
.field-label { display: block; margin-bottom: 0.35rem; color: #4b5563; font-size: 0.75rem; font-weight: 650; }
.dark .field-label { color: #d1d5db; }

.key-option { display: flex; width: 100%; min-width: 0; align-items: flex-start; justify-content: space-between; gap: 0.5rem; }
.key-option__name { overflow: hidden; font-size: 0.8rem; font-weight: 650; text-overflow: ellipsis; white-space: nowrap; }
.key-option__meta { margin-top: 0.15rem; overflow: hidden; color: #6b7280; font-size: 0.68rem; text-overflow: ellipsis; white-space: nowrap; }
.key-summary { margin-top: 0.75rem; padding-top: 0.65rem; border-top: 1px solid #e5e7eb; }
.dark .key-summary { border-color: #374151; }
.key-summary__row { display: flex; min-width: 0; align-items: center; justify-content: space-between; gap: 0.75rem; padding: 0.18rem 0; color: #6b7280; font-size: 0.7rem; }
.dark .key-summary__row { color: #9ca3af; }
.key-summary__row strong { min-width: 0; overflow: hidden; color: #1f2937; font-weight: 650; text-overflow: ellipsis; white-space: nowrap; }
.dark .key-summary__row strong { color: #e5e7eb; }

.execution-mode { position: relative; display: flex; align-items: flex-start; gap: 0.65rem; margin-top: 0.75rem; padding: 0.7rem; border: 1px solid #99f6e4; border-radius: 6px; background: #f0fdfa; color: #115e59; }
.execution-mode.is-async { border-color: #fcd34d; background: #fffbeb; color: #92400e; }
.execution-mode.is-pending { opacity: 0.72; }
.dark .execution-mode { border-color: rgba(45, 212, 191, 0.35); background: rgba(13, 148, 136, 0.12); color: #99f6e4; }
.dark .execution-mode.is-async { border-color: rgba(251, 191, 36, 0.4); background: rgba(146, 64, 14, 0.18); color: #fde68a; }
.execution-mode__icon { display: grid; width: 2rem; height: 2rem; flex: 0 0 auto; place-items: center; border: 1px solid currentColor; border-radius: 6px; }
.execution-mode strong { display: block; font-size: 0.8rem; }
.execution-mode p { margin-top: 0.15rem; padding-right: 1rem; font-size: 0.68rem; line-height: 1.4; }
.execution-mode__lock { position: absolute; top: 0.5rem; right: 0.5rem; opacity: 0.7; }

.reference-drop { display: flex; min-height: 6.5rem; cursor: pointer; flex-direction: column; align-items: center; justify-content: center; gap: 0.3rem; border: 1px dashed #9ca3af; border-radius: 6px; color: #0f766e; text-align: center; font-size: 0.75rem; font-weight: 650; }
.reference-drop small { color: #6b7280; font-size: 0.65rem; font-weight: 400; }
.reference-drop:hover { border-color: #0d9488; background: #f0fdfa; }
.dark .reference-drop:hover { background: rgba(13, 148, 136, 0.1); }
.reference-drop.is-disabled { cursor: not-allowed; opacity: 0.55; }
.reference-list { display: flex; flex-direction: column; gap: 0.4rem; margin-top: 0.6rem; }
.reference-item { display: grid; grid-template-columns: 2rem minmax(0, 1fr) 1.5rem; align-items: center; gap: 0.45rem; padding: 0.3rem; border: 1px solid #e5e7eb; border-radius: 6px; }
.dark .reference-item { border-color: #374151; }
.reference-item img { width: 2rem; height: 2rem; border-radius: 4px; object-fit: cover; }
.reference-item span { overflow: hidden; font-size: 0.68rem; text-overflow: ellipsis; white-space: nowrap; }
.reference-item button { display: grid; width: 1.5rem; height: 1.5rem; place-items: center; border-radius: 4px; color: #6b7280; }
.reference-item button:hover { background: #fee2e2; color: #b91c1c; }

.prompt-input { width: 100%; min-height: 9rem; resize: vertical; line-height: 1.6; }
.prompt-footer { display: flex; justify-content: space-between; gap: 1rem; margin-top: 0.3rem; color: #9ca3af; font-size: 0.65rem; }
.field-error { color: #b91c1c; }
.parameter-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 0.65rem; margin-top: 0.9rem; }
.parameter-field { min-width: 0; }
.parameter-field > span { display: block; margin-bottom: 0.3rem; color: #4b5563; font-size: 0.7rem; font-weight: 650; }
.dark .parameter-field > span { color: #d1d5db; }
.parameter-field .input { width: 100%; }
.generate-row { display: flex; align-items: center; justify-content: flex-end; gap: 0.5rem; margin-top: 0.9rem; padding-top: 0.8rem; border-top: 1px solid #e5e7eb; }
.dark .generate-row { border-color: #374151; }
.generate-button { display: inline-flex; min-width: 9.5rem; min-height: 2.5rem; align-items: center; justify-content: center; gap: 0.45rem; padding: 0.55rem 1rem; border-radius: 6px; background: #0f766e; color: #fff; font-size: 0.8rem; font-weight: 750; }
.generate-button:hover:not(:disabled) { background: #115e59; }
.generate-button.is-async { background: #b45309; }
.generate-button.is-async:hover:not(:disabled) { background: #92400e; }
.generate-button:disabled { cursor: not-allowed; opacity: 0.5; }
.protocol-badge { padding: 0.2rem 0.45rem; border: 1px solid #d1d5db; border-radius: 4px; color: #4b5563; font-size: 0.65rem; font-weight: 700; white-space: nowrap; }
.dark .protocol-badge { border-color: #4b5563; color: #d1d5db; }

.workbench-alert { display: flex; align-items: flex-start; gap: 0.4rem; margin-top: 0.65rem; padding: 0.55rem 0.65rem; border-radius: 6px; font-size: 0.72rem; line-height: 1.45; }
.workbench-alert.is-error { border: 1px solid #fecaca; background: #fef2f2; color: #991b1b; }
.dark .workbench-alert.is-error { border-color: rgba(248, 113, 113, 0.35); background: rgba(127, 29, 29, 0.18); color: #fca5a5; }
.unknown-submission { display: flex; align-items: center; justify-content: space-between; gap: 1rem; border-left: 3px solid #d97706; }
.unknown-submission strong { color: #92400e; font-size: 0.8rem; }
.dark .unknown-submission strong { color: #fcd34d; }
.unknown-submission p { margin-top: 0.2rem; color: #6b7280; font-size: 0.7rem; line-height: 1.45; }
.unknown-submission code { display: block; margin-top: 0.35rem; overflow-wrap: anywhere; color: #92400e; font-size: 0.65rem; }
.workbench-spinner { display: inline-block; width: 1rem; height: 1rem; flex: 0 0 auto; border: 2px solid #99f6e4; border-top-color: #0f766e; border-radius: 50%; animation: workbench-spin 0.75s linear infinite; }
.workbench-spinner.is-light { border-color: rgba(255,255,255,0.4); border-top-color: #fff; }

.async-runtime { border-left: 3px solid #d97706; }
.async-runtime__header { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; }
.async-runtime__eyebrow { color: #b45309; font-size: 0.65rem; font-weight: 750; text-transform: uppercase; }
.async-runtime h2 { margin-top: 0.15rem; font-size: 0.95rem; font-weight: 750; }
.async-runtime__id { display: flex; min-width: 0; align-items: center; gap: 0.4rem; padding: 0.35rem 0.45rem; border: 1px solid #e5e7eb; border-radius: 6px; }
.dark .async-runtime__id { border-color: #374151; }
.async-runtime__id code { max-width: 300px; overflow: hidden; font-size: 0.68rem; text-overflow: ellipsis; white-space: nowrap; }
.task-track { display: grid; grid-template-columns: repeat(3, 1fr); gap: 0.3rem; margin-top: 0.9rem; }
.task-stage { display: flex; min-width: 0; flex-direction: column; align-items: center; gap: 0.3rem; color: #9ca3af; text-align: center; }
.task-stage span { display: grid; width: 1.5rem; height: 1.5rem; place-items: center; border: 1px solid #d1d5db; border-radius: 50%; }
.task-stage strong { width: 100%; overflow: hidden; font-size: 0.62rem; text-overflow: ellipsis; white-space: nowrap; }
.task-stage.active { color: #b45309; }
.task-stage.done { color: #047857; }
.task-stage.failed { color: #b91c1c; }
.task-progress { height: 0.3rem; margin-top: 0.75rem; overflow: hidden; border-radius: 3px; background: #e5e7eb; }
.dark .task-progress { background: #374151; }
.task-progress span { display: block; height: 100%; border-radius: 3px; background: #d97706; transition: width 0.2s ease; }
.async-runtime__footer { display: flex; align-items: center; justify-content: space-between; gap: 1rem; margin-top: 0.75rem; color: #6b7280; font-size: 0.7rem; }
.async-runtime__actions { display: flex; flex-wrap: wrap; align-items: center; justify-content: flex-end; gap: 0.65rem; }
.text-link { color: #0f766e; font-size: 0.72rem; font-weight: 700; }
.text-link:hover { text-decoration: underline; }

.result-empty { display: flex; min-height: 15rem; flex-direction: column; align-items: center; justify-content: center; gap: 0.45rem; border: 1px dashed #d1d5db; border-radius: 6px; color: #6b7280; text-align: center; }
.dark .result-empty { border-color: #374151; color: #9ca3af; }
.result-empty strong { color: #4b5563; font-size: 0.8rem; }
.dark .result-empty strong { color: #d1d5db; }
.result-empty span { font-size: 0.7rem; }
.result-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(210px, 1fr)); gap: 0.65rem; }
.result-item { overflow: hidden; border: 1px solid #e5e7eb; border-radius: 6px; }
.dark .result-item { border-color: #374151; }
.result-item__media { display: block; width: 100%; aspect-ratio: 1 / 1; padding: 0; border: 0; cursor: zoom-in; background: #f3f4f6; }
.dark .result-item__media { background: #030712; }
.result-item__media img { width: 100%; height: 100%; object-fit: contain; }
.result-item__footer { display: flex; min-height: 2.6rem; align-items: center; gap: 0.45rem; padding: 0.4rem; }
.archive-state { display: inline-flex; min-width: 0; flex: 1; align-items: center; gap: 0.35rem; color: #6b7280; font-size: 0.67rem; }
.archive-state.is-archived,
.archive-state.is-pending_review { color: #047857; }
.archive-state.is-local { color: #0f766e; }
.archive-state.is-failed { color: #b91c1c; }

@keyframes workbench-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .workbench-spinner { animation: none; } .task-progress span { transition: none; } }
@media (max-width: 1240px) {
  .workbench-grid { grid-template-columns: minmax(230px, 0.8fr) minmax(420px, 1.4fr); }
  .workbench-column--library { grid-column: 1 / -1; }
}
@media (max-width: 720px) {
  .workbench-grid { grid-template-columns: 1fr; }
  .workbench-column--library { grid-column: auto; }
  .parameter-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .workbench-header { flex-direction: column; }
  .workbench-header__actions { justify-content: flex-start; }
}
@media (max-width: 520px) {
  .parameter-grid { grid-template-columns: 1fr; }
  .generate-row { align-items: stretch; flex-direction: column-reverse; }
  .generate-button { width: 100%; }
  .async-runtime__header { flex-direction: column; }
  .async-runtime__id { width: 100%; justify-content: space-between; }
  .task-track { grid-template-columns: repeat(5, minmax(48px, 1fr)); overflow-x: auto; padding-bottom: 0.25rem; }
  .async-runtime__footer { align-items: flex-start; flex-direction: column; }
  .async-runtime__actions { width: 100%; justify-content: flex-start; }
  .unknown-submission { align-items: stretch; flex-direction: column; }
}
</style>
