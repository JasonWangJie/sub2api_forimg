<template>
  <section class="library-panel" :class="{ 'library-panel--compact': compact }" aria-labelledby="image-library-heading">
    <header class="library-panel__header">
      <div class="min-w-0">
        <div class="library-panel__title-row">
          <h2 id="image-library-heading" class="library-panel__title">{{ t('imageWorkflow.library.title') }}</h2>
          <span v-if="!compact" class="library-storage-chip" :title="t('imageWorkflow.library.storageLead')">
            <Icon name="database" size="xs" />
            {{ t('imageWorkflow.library.storageBadge') }}
          </span>
        </div>
        <p v-if="!compact" class="library-panel__description">{{ t('imageWorkflow.library.description') }}</p>
      </div>
      <div class="flex items-center gap-2">
        <RouterLink v-if="compact" to="/image-library" class="library-icon-button" :aria-label="t('imageWorkflow.library.openFull')" :title="t('imageWorkflow.library.openFull')">
          <Icon name="externalLink" size="sm" />
        </RouterLink>
        <button type="button" class="library-icon-button" :disabled="loading" :aria-label="t('common.refresh')" :title="t('common.refresh')" @click="refresh">
          <Icon name="refresh" size="sm" :class="loading && 'animate-spin'" />
        </button>
      </div>
    </header>

    <aside
      class="library-storage"
      :class="{ 'library-storage--compact': compact }"
      :aria-label="t('imageWorkflow.library.storageTitle')"
    >
      <template v-if="compact">
        <p class="library-storage__compact-line" :title="compactStorageHint">
          <Icon name="database" size="xs" />
          <span>{{ t('imageWorkflow.library.storageCompact') }}</span>
        </p>
      </template>
      <template v-else>
        <div class="library-storage__glow" aria-hidden="true"></div>
        <div class="library-storage__head">
          <span class="library-storage__mark" aria-hidden="true">
            <Icon name="infoCircle" size="sm" />
          </span>
          <div class="min-w-0">
            <h3 id="library-storage-heading" class="library-storage__title">{{ t('imageWorkflow.library.storageTitle') }}</h3>
            <p class="library-storage__lead">{{ t('imageWorkflow.library.storageLead') }}</p>
          </div>
        </div>
        <ul class="library-storage__rules" aria-labelledby="library-storage-heading">
          <li v-for="rule in storageRules" :key="rule.key" class="library-storage__rule" :class="`is-${rule.tone}`">
            <Icon :name="rule.icon" size="xs" />
            <span>{{ rule.label }}</span>
          </li>
        </ul>
      </template>
    </aside>

    <section v-if="submissions.length" class="library-recovery" aria-labelledby="image-library-submission-heading">
      <div class="library-recovery__heading">
        <span class="library-recovery__icon"><Icon name="checkCircle" size="sm" /></span>
        <div class="min-w-0">
          <h3 id="image-library-submission-heading">
            {{ t('imageWorkflow.library.submissionQueueTitle', { count: submissions.length }) }}
          </h3>
          <p>{{ t('imageWorkflow.library.submissionQueueHint') }}</p>
        </div>
      </div>
      <div class="library-recovery__list">
        <article v-for="submission in visibleSubmissions" :key="submission.id" class="library-recovery__item">
          <span class="library-recovery__preview">
            <img v-if="submissionPreviewURLs[submission.id]" :src="submissionPreviewURLs[submission.id]" :alt="submission.title" />
            <Icon v-else name="inbox" size="sm" />
          </span>
          <div class="min-w-0 flex-1">
            <strong :title="submission.title">{{ submission.title || t('imageWorkflow.library.untitled') }}</strong>
            <small>{{ t(`imageWorkflow.archive.${submission.status === 'approved_pending_sync' ? 'approved_pending_sync' : 'pending_review'}`) }}</small>
          </div>
          <button
            v-if="submission.status === 'approved_pending_sync'"
            type="button"
            class="library-recovery__retry"
            :disabled="submissionBusyId === submission.id"
            @click="syncSubmission(submission)"
          >
            <Icon name="upload" size="xs" :class="submissionBusyId === submission.id && 'animate-spin'" />
            {{ t('imageWorkflow.workbench.syncToPlaza') }}
          </button>
          <button
            type="button"
            class="library-action"
            :disabled="submissionBusyId === submission.id"
            :title="t('imageWorkflow.library.withdrawSubmission')"
            :aria-label="t('imageWorkflow.library.withdrawSubmission')"
            @click="withdrawSubmission(submission)"
          >
            <Icon name="x" size="sm" />
          </button>
        </article>
      </div>
    </section>

    <div v-if="loading && !items.length" class="library-empty" role="status">
      <span class="library-spinner" aria-hidden="true"></span>
      {{ t('common.loading') }}
    </div>
    <div v-else-if="error && !items.length" class="library-empty library-empty--error" role="alert">
      <Icon name="exclamationCircle" size="lg" />
      <span>{{ error }}</span>
      <button type="button" class="btn btn-secondary" @click="refresh">{{ t('common.refresh') }}</button>
    </div>
    <div v-else-if="!items.length" class="library-empty">
      <Icon name="inbox" size="lg" />
      <span>{{ t('imageWorkflow.library.empty') }}</span>
    </div>

    <div v-else class="library-grid" :class="{ 'library-grid--compact': compact }">
      <article v-for="item in visibleItems" :key="item.id" class="library-item">
        <button type="button" class="library-item__media" @click="openLightbox(item)">
          <LazyImage
            class="library-item__lazy"
            :src="previewURLs[item.id] || undefined"
            :alt="item.title || t('imageWorkflow.library.untitled')"
            :load="() => ensurePreview(item)"
            @error="markBroken(item.id)"
          >
            <template #error>
              <span class="library-item__broken">
                <Icon name="exclamationTriangle" size="lg" />
                {{ t('imageWorkflow.library.imageUnavailable') }}
              </span>
            </template>
            <template #placeholder>
              <span class="library-item__broken"><span class="library-spinner" aria-hidden="true"></span></span>
            </template>
          </LazyImage>
          <span class="library-item__mode is-realtime">
            <Icon name="bolt" size="xs" />
            {{ t('imageWorkflow.mode.realtime') }}
          </span>
        </button>
        <div class="library-item__body">
          <div class="library-item__title-row">
            <div v-if="editingId === item.id" class="library-title-editor">
              <input
                v-model="editingTitle"
                type="text"
                maxlength="200"
                class="input"
                :aria-label="t('imageWorkflow.library.privateTitle')"
                :disabled="busyId === item.id"
                @keydown.enter.prevent="saveTitle(item)"
                @keydown.esc.prevent="cancelTitleEdit"
              />
              <button type="button" class="library-action" :disabled="busyId === item.id" :title="t('common.save')" :aria-label="t('common.save')" @click="saveTitle(item)">
                <Icon name="check" size="xs" />
              </button>
              <button type="button" class="library-action" :disabled="busyId === item.id" :title="t('common.cancel')" :aria-label="t('common.cancel')" @click="cancelTitleEdit">
                <Icon name="x" size="xs" />
              </button>
            </div>
            <template v-else>
              <h3 class="library-item__title" :title="item.title">{{ item.title || t('imageWorkflow.library.untitled') }}</h3>
              <button type="button" class="library-title-edit" :title="t('imageWorkflow.library.editPrivateTitle')" :aria-label="t('imageWorkflow.library.editPrivateTitle')" @click="startTitleEdit(item)">
                <Icon name="edit" size="xs" />
              </button>
            </template>
            <span class="library-status" :class="statusClass(submissionStatusFor(item))">
              {{ publicationLabel(submissionStatusFor(item)) }}
            </span>
          </div>
          <p class="library-item__meta">{{ item.platform || '—' }} · {{ item.model || '—' }}</p>
          <p v-if="!compact && item.prompt" class="library-item__prompt">{{ item.prompt }}</p>
          <div class="library-item__actions">
            <button type="button" class="library-action" :title="t('imageWorkflow.library.reuse')" :aria-label="t('imageWorkflow.library.reuse')" @click="emitReuse(item)">
              <Icon name="refresh" size="sm" />
            </button>
            <a
              class="library-action"
              :href="previewURLs[item.id] || '#'"
              :download="item.fileName || 'image.png'"
              :title="t('imageWorkbench.download')"
              :aria-label="t('imageWorkbench.download')"
            >
              <Icon name="download" size="sm" />
            </a>
            <button
              v-if="canPublish(item)"
              type="button"
              class="library-text-action"
              :disabled="busyId === item.id"
              @click="publish(item)"
            >
              {{ t('imageWorkflow.library.submitReview') }}
            </button>
            <button
              v-else-if="canSync(item)"
              type="button"
              class="library-text-action"
              :disabled="busyId === item.id"
              @click="syncItem(item)"
            >
              {{ t('imageWorkflow.workbench.syncToPlaza') }}
            </button>
            <button type="button" class="library-action is-danger" :disabled="busyId === item.id" :title="t('common.delete')" :aria-label="t('common.delete')" @click="remove(item)">
              <Icon name="trash" size="sm" />
            </button>
          </div>
        </div>
      </article>
    </div>

    <ImageLightbox
      :src="lightboxSrc"
      :alt="lightboxAlt"
      @close="lightboxSrc = ''"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import ImageLightbox from '@/components/common/ImageLightbox.vue'
import LazyImage from '@/components/common/LazyImage.vue'
import { useAppStore, useAuthStore } from '@/stores'
import {
  createPlazaSubmissionRequest,
  listMyPlazaSubmissionRequests,
  syncPlazaSubmissionRequest,
  withdrawPlazaSubmissionRequest,
} from '@/api/imageLibrary'
import {
  bindPersonalGalleryRequestId,
  getPersonalGalleryItem,
  listPersonalGalleryItems,
  onPersonalGalleryChanged,
  removePersonalGalleryItem,
  updatePersonalGalleryItem,
  type PersonalGalleryRecord,
} from './personalGalleryStore'
import type { ImageLibraryItem, ImagePlazaSubmissionRequest, ImagePublicationStatus } from './types'

const props = withDefaults(defineProps<{ compact?: boolean; limit?: number }>(), {
  compact: false,
  limit: 24,
})
const emit = defineEmits<{
  (event: 'reuse', item: ImageLibraryItem): void
  (event: 'view', item: ImageLibraryItem): void
}>()

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const loading = ref(false)
const error = ref('')
const items = ref<PersonalGalleryRecord[]>([])
const busyId = ref('')
const brokenImages = ref(new Set<string>())
const previewURLs = ref<Record<string, string>>({})
const lightboxSrc = ref('')
const lightboxAlt = ref('')
const submissions = ref<ImagePlazaSubmissionRequest[]>([])
const submissionPreviewURLs = ref<Record<string, string>>({})
const submissionBusyId = ref('')
const editingId = ref('')
const editingTitle = ref('')
let stopGalleryListener: (() => void) | null = null

const visibleSubmissions = computed(() => props.compact ? submissions.value.slice(0, 3) : submissions.value)
const visibleItems = computed(() => props.compact ? items.value.slice(0, props.limit) : items.value)
const storageRules = computed(() => [
  { key: 'local', icon: 'database' as const, tone: 'teal', label: t('imageWorkflow.library.storageRules.local') },
  { key: 'realtime', icon: 'bolt' as const, tone: 'teal', label: t('imageWorkflow.library.storageRules.realtime') },
  { key: 'retention', icon: 'clock' as const, tone: 'amber', label: t('imageWorkflow.library.storageRules.retention') },
  { key: 'asyncSkip', icon: 'x' as const, tone: 'slate', label: t('imageWorkflow.library.storageRules.asyncSkip') },
  { key: 'device', icon: 'exclamationTriangle' as const, tone: 'amber', label: t('imageWorkflow.library.storageRules.device') },
])
const compactStorageHint = computed(() => [
  t('imageWorkflow.library.storageRules.local'),
  t('imageWorkflow.library.storageRules.realtime'),
  t('imageWorkflow.library.storageRules.retention'),
  t('imageWorkflow.library.storageRules.asyncSkip'),
  t('imageWorkflow.library.storageRules.device'),
].join(' · '))

function toLibraryItem(record: PersonalGalleryRecord): ImageLibraryItem {
  const status = submissionStatusFor(record)
  return {
    id: record.id,
    asset_id: record.id,
    source: 'realtime_import',
    platform: record.platform || '',
    execution_mode: 'realtime',
    model: record.model || '',
    title: record.title,
    prompt: record.prompt || null,
    requested_size: record.requestedSize || null,
    aspect_ratio: record.aspectRatio || null,
    quality: record.quality || null,
    output_format: record.outputFormat || null,
    byte_size: record.byteSize,
    visibility: 'private',
    archive_status: 'local',
    publication_status: status,
    view_url: previewURLs.value[record.id] || '',
    preview_url: previewURLs.value[record.id] || null,
    created_at: new Date(record.createdAt).toISOString(),
    expires_at: new Date(record.expiresAt).toISOString(),
  }
}

function submissionFor(item: PersonalGalleryRecord): ImagePlazaSubmissionRequest | undefined {
  if (item.requestId) {
    const byId = submissions.value.find((entry) => entry.id === item.requestId)
    if (byId) return byId
  }
  return submissions.value.find((entry) => entry.client_blob_key === item.id)
}

function submissionStatusFor(item: PersonalGalleryRecord): ImagePublicationStatus | null {
  const submission = submissionFor(item)
  if (!submission) return null
  if (submission.status === 'pending_review') return 'pending_review'
  if (submission.status === 'approved_pending_sync') return 'pending_review'
  if (submission.status === 'synced') return 'published'
  if (submission.status === 'rejected') return 'rejected'
  if (submission.status === 'withdrawn') return 'withdrawn'
  return null
}

function revokeObjectURLs(urls: Record<string, string>) {
  Object.values(urls).forEach((url) => {
    if (url.startsWith('blob:')) URL.revokeObjectURL(url)
  })
}

async function refreshPreviews(records: PersonalGalleryRecord[]) {
  const previous = previewURLs.value
  const next: Record<string, string> = {}
  for (const item of records) {
    if (previous[item.id]) {
      next[item.id] = previous[item.id]
      continue
    }
    if (item.file) next[item.id] = URL.createObjectURL(item.file)
  }
  Object.entries(previous).forEach(([id, url]) => {
    if (!next[id] && url.startsWith('blob:')) URL.revokeObjectURL(url)
  })
  previewURLs.value = next
}

async function refresh() {
  loading.value = true
  error.value = ''
  try {
    const userId = Number(authStore.user?.id || 0)
    items.value = await listPersonalGalleryItems(userId)
    await refreshPreviews(items.value)
    await refreshSubmissions()
  } catch (cause: any) {
    error.value = cause?.message || t('imageWorkflow.library.loadFailed')
  } finally {
    loading.value = false
  }
}

async function refreshSubmissions() {
  try {
    const [pending, approved] = await Promise.all([
      listMyPlazaSubmissionRequests({ status: 'pending_review', limit: 50 }),
      listMyPlazaSubmissionRequests({ status: 'approved_pending_sync', limit: 50 }),
    ])
    submissions.value = [...approved.items, ...pending.items]
    const nextPreviews: Record<string, string> = {}
    for (const item of submissions.value) {
      const local = await getPersonalGalleryItem(item.client_blob_key)
      if (local?.file) nextPreviews[item.id] = URL.createObjectURL(local.file)
      else if (previewURLs.value[item.client_blob_key]) nextPreviews[item.id] = previewURLs.value[item.client_blob_key]
    }
    revokeObjectURLs(submissionPreviewURLs.value)
    submissionPreviewURLs.value = nextPreviews
  } catch {
    submissions.value = []
  }
}

async function ensurePreview(item: PersonalGalleryRecord): Promise<string> {
  if (brokenImages.value.has(item.id)) throw new Error('image unavailable')
  if (previewURLs.value[item.id]) return previewURLs.value[item.id]
  const fresh = await getPersonalGalleryItem(item.id)
  if (!fresh?.file) {
    markBroken(item.id)
    throw new Error('image unavailable')
  }
  const url = URL.createObjectURL(fresh.file)
  previewURLs.value = { ...previewURLs.value, [item.id]: url }
  return url
}

function markBroken(id: string) {
  brokenImages.value = new Set([...brokenImages.value, id])
}

async function openLightbox(item: PersonalGalleryRecord) {
  try {
    const url = await ensurePreview(item)
    lightboxSrc.value = url
    lightboxAlt.value = item.title || t('imageWorkflow.library.untitled')
    emit('view', toLibraryItem(item))
  } catch {
    // ignore
  }
}

function emitReuse(item: PersonalGalleryRecord) {
  emit('reuse', toLibraryItem(item))
}

function canPublish(item: PersonalGalleryRecord) {
  const status = submissionFor(item)?.status
  return !status || status === 'rejected' || status === 'withdrawn'
}

function canSync(item: PersonalGalleryRecord) {
  return submissionFor(item)?.status === 'approved_pending_sync'
}

async function publish(item: PersonalGalleryRecord) {
  if (!window.confirm(t('imageWorkflow.library.publishConfirm'))) return
  busyId.value = item.id
  try {
    const sharePrompt = !props.compact && Boolean(item.prompt) && window.confirm(t('imageWorkflow.library.sharePromptConfirm'))
    const record = await getPersonalGalleryItem(item.id)
    if (!record?.file) throw new Error(t('imageWorkflow.workbench.localResultUnavailable'))
    const created = await createPlazaSubmissionRequest({
      title: record.title,
      private_prompt: record.prompt,
      public_title: record.title,
      share_prompt: sharePrompt,
      public_prompt: sharePrompt ? record.prompt || undefined : undefined,
      api_key_id: record.apiKeyId,
      group_id: record.groupId ?? undefined,
      platform: record.platform || 'openai',
      generation_mode: 'realtime',
      source_type: 'realtime_import',
      model: record.model || '',
      requested_size: record.requestedSize,
      aspect_ratio: record.aspectRatio,
      quality: record.quality,
      content_type: record.contentType,
      byte_size: record.byteSize,
      checksum_sha256: record.checksumSha256,
      client_blob_key: record.id,
    }, record.id)
    await bindPersonalGalleryRequestId(record.id, created.id)
    appStore.showSuccess(t('imageWorkflow.library.submitted'))
    await refresh()
  } catch (cause: any) {
    appStore.showError(cause?.message || t('imageWorkflow.library.actionFailed'))
  } finally {
    busyId.value = ''
  }
}

async function syncItem(item: PersonalGalleryRecord) {
  const submission = submissionFor(item)
  if (!submission) return
  await syncSubmission(submission)
}

async function syncSubmission(submission: ImagePlazaSubmissionRequest) {
  if (!window.confirm(t('imageWorkflow.workbench.syncConfirm'))) return
  submissionBusyId.value = submission.id
  busyId.value = submission.client_blob_key
  try {
    const blob = await getPersonalGalleryItem(submission.client_blob_key)
    if (!blob?.file) throw new Error(t('imageWorkflow.workbench.localResultUnavailable'))
    const file = blob.file instanceof File
      ? blob.file
      : new File([blob.file], blob.fileName || 'sync-image.png', { type: blob.contentType || blob.file.type })
    await syncPlazaSubmissionRequest(submission.id, file, file.name)
    appStore.showSuccess(t('imageWorkflow.workbench.syncSuccess'))
    await refresh()
  } catch (cause: any) {
    appStore.showError(cause?.message || t('imageWorkflow.workbench.syncFailed'))
  } finally {
    submissionBusyId.value = ''
    busyId.value = ''
  }
}

async function withdrawSubmission(submission: ImagePlazaSubmissionRequest) {
  if (!window.confirm(t('imageWorkflow.library.withdrawSubmissionConfirm'))) return
  submissionBusyId.value = submission.id
  try {
    await withdrawPlazaSubmissionRequest(submission.id)
    appStore.showSuccess(t('imageWorkflow.library.withdrawn'))
    await refreshSubmissions()
  } catch (cause: any) {
    appStore.showError(cause?.message || t('imageWorkflow.library.actionFailed'))
  } finally {
    submissionBusyId.value = ''
  }
}

async function remove(item: PersonalGalleryRecord) {
  if (!window.confirm(t('imageWorkflow.library.deleteConfirm'))) return
  busyId.value = item.id
  try {
    await removePersonalGalleryItem(item.id)
    items.value = items.value.filter((candidate) => candidate.id !== item.id)
    const url = previewURLs.value[item.id]
    if (url?.startsWith('blob:')) URL.revokeObjectURL(url)
    const next = { ...previewURLs.value }
    delete next[item.id]
    previewURLs.value = next
    appStore.showSuccess(t('common.deleted'))
  } catch (cause: any) {
    appStore.showError(cause?.message || t('imageWorkflow.library.actionFailed'))
  } finally {
    busyId.value = ''
  }
}

function startTitleEdit(item: PersonalGalleryRecord) {
  editingId.value = item.id
  editingTitle.value = item.title || ''
}

function cancelTitleEdit() {
  editingId.value = ''
  editingTitle.value = ''
}

async function saveTitle(item: PersonalGalleryRecord) {
  if (busyId.value) return
  busyId.value = item.id
  try {
    const updated = await updatePersonalGalleryItem(item.id, { title: editingTitle.value.trim() })
    if (updated) {
      const index = items.value.findIndex((candidate) => candidate.id === item.id)
      if (index >= 0) items.value[index] = updated
    }
    cancelTitleEdit()
    appStore.showSuccess(t('imageWorkflow.library.titleUpdated'))
  } catch (cause: any) {
    appStore.showError(cause?.message || t('imageWorkflow.library.actionFailed'))
  } finally {
    busyId.value = ''
  }
}

function publicationLabel(status?: ImagePublicationStatus | null) {
  if (!status) return t('imageWorkflow.library.private')
  return t(`imageWorkflow.publication.${status}`)
}

function statusClass(status?: ImagePublicationStatus | null) {
  if (status === 'published') return 'is-published'
  if (status === 'pending_review') return 'is-pending'
  if (status === 'rejected' || status === 'admin_hidden') return 'is-blocked'
  return 'is-private'
}

onMounted(() => {
  stopGalleryListener = onPersonalGalleryChanged(() => { void refresh() })
  void refresh()
})

onUnmounted(() => {
  stopGalleryListener?.()
  revokeObjectURLs(previewURLs.value)
  revokeObjectURLs(submissionPreviewURLs.value)
})

defineExpose({ refresh })
</script>

<style scoped>
.library-panel {
  min-width: 0;
}

.library-panel__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 0.75rem;
  margin-bottom: 0.75rem;
}

.library-panel__title-row {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.45rem;
}

.library-panel__title {
  color: #111827;
  font-size: 0.95rem;
  font-weight: 700;
  line-height: 1.4;
}

.dark .library-panel__title { color: #f3f4f6; }
.library-panel__description { margin-top: 0.2rem; color: #6b7280; font-size: 0.8rem; }
.dark .library-panel__description { color: #9ca3af; }

.library-storage-chip {
  display: inline-flex;
  align-items: center;
  gap: 0.28rem;
  padding: 0.18rem 0.5rem;
  border: 1px solid rgba(13, 148, 136, 0.35);
  border-radius: 999px;
  background:
    linear-gradient(135deg, rgba(240, 253, 250, 0.95), rgba(204, 251, 241, 0.55));
  color: #0f766e;
  font-size: 0.62rem;
  font-weight: 750;
  letter-spacing: 0.02em;
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.7);
}

.dark .library-storage-chip {
  border-color: rgba(45, 212, 191, 0.28);
  background: linear-gradient(135deg, rgba(19, 78, 74, 0.55), rgba(13, 148, 136, 0.18));
  color: #5eead4;
  box-shadow: none;
}

.library-storage {
  --storage-ink: #134e4a;
  --storage-muted: #0f766e;
  position: relative;
  overflow: hidden;
  margin-bottom: 0.9rem;
  padding: 0.75rem 0.8rem 0.7rem;
  border: 1px solid rgba(45, 212, 191, 0.28);
  border-radius: 10px;
  background:
    linear-gradient(145deg, rgba(240, 253, 250, 0.92) 0%, rgba(255, 255, 255, 0.88) 48%, rgba(236, 254, 255, 0.9) 100%);
  box-shadow:
    0 1px 0 rgba(255, 255, 255, 0.8) inset,
    0 8px 24px -18px rgba(15, 118, 110, 0.45);
  animation: library-storage-in 420ms cubic-bezier(0.22, 1, 0.36, 1) both;
}

.dark .library-storage {
  --storage-ink: #ccfbf1;
  --storage-muted: #99f6e4;
  border-color: rgba(45, 212, 191, 0.22);
  background:
    linear-gradient(145deg, rgba(17, 24, 39, 0.92) 0%, rgba(19, 78, 74, 0.28) 55%, rgba(17, 24, 39, 0.95) 100%);
  box-shadow: 0 10px 28px -20px rgba(0, 0, 0, 0.65);
}

.library-storage__glow {
  position: absolute;
  inset: auto -20% -55% auto;
  width: 9rem;
  height: 9rem;
  border-radius: 50%;
  background: radial-gradient(circle, rgba(45, 212, 191, 0.28), transparent 68%);
  pointer-events: none;
}

.dark .library-storage__glow {
  background: radial-gradient(circle, rgba(45, 212, 191, 0.18), transparent 68%);
}

.library-storage__head {
  position: relative;
  display: flex;
  align-items: flex-start;
  gap: 0.55rem;
}

.library-storage__mark {
  display: grid;
  width: 1.85rem;
  height: 1.85rem;
  flex: 0 0 auto;
  place-items: center;
  border-radius: 8px;
  border: 1px solid rgba(13, 148, 136, 0.28);
  background: linear-gradient(160deg, #ccfbf1, #99f6e4);
  color: #0f766e;
  box-shadow: 0 4px 10px -6px rgba(15, 118, 110, 0.55);
}

.dark .library-storage__mark {
  border-color: rgba(45, 212, 191, 0.28);
  background: linear-gradient(160deg, rgba(13, 148, 136, 0.45), rgba(15, 118, 110, 0.2));
  color: #5eead4;
  box-shadow: none;
}

.library-storage__title {
  color: var(--storage-ink);
  font-size: 0.76rem;
  font-weight: 750;
  letter-spacing: 0.01em;
}

.library-storage__lead {
  margin-top: 0.18rem;
  color: var(--storage-muted);
  font-size: 0.68rem;
  line-height: 1.45;
}

.dark .library-storage__lead { color: #99f6e4; opacity: 0.88; }

.library-storage__rules {
  position: relative;
  display: flex;
  flex-wrap: wrap;
  gap: 0.35rem;
  margin-top: 0.65rem;
}

.library-storage__rule {
  display: inline-flex;
  max-width: 100%;
  align-items: center;
  gap: 0.28rem;
  padding: 0.28rem 0.55rem;
  border-radius: 999px;
  border: 1px solid transparent;
  font-size: 0.64rem;
  font-weight: 650;
  line-height: 1.2;
  animation: library-rule-in 480ms cubic-bezier(0.22, 1, 0.36, 1) both;
}

.library-storage__rule:nth-child(1) { animation-delay: 40ms; }
.library-storage__rule:nth-child(2) { animation-delay: 80ms; }
.library-storage__rule:nth-child(3) { animation-delay: 120ms; }
.library-storage__rule:nth-child(4) { animation-delay: 160ms; }
.library-storage__rule:nth-child(5) { animation-delay: 200ms; }

.library-storage__rule.is-teal {
  border-color: rgba(13, 148, 136, 0.22);
  background: rgba(240, 253, 250, 0.95);
  color: #0f766e;
}
.library-storage__rule.is-amber {
  border-color: rgba(217, 119, 6, 0.24);
  background: rgba(255, 251, 235, 0.95);
  color: #b45309;
}
.library-storage__rule.is-slate {
  border-color: rgba(100, 116, 139, 0.24);
  background: rgba(248, 250, 252, 0.95);
  color: #475569;
}

.dark .library-storage__rule.is-teal {
  border-color: rgba(45, 212, 191, 0.28);
  background: rgba(13, 148, 136, 0.16);
  color: #5eead4;
}
.dark .library-storage__rule.is-amber {
  border-color: rgba(251, 191, 36, 0.28);
  background: rgba(146, 64, 14, 0.22);
  color: #fcd34d;
}
.dark .library-storage__rule.is-slate {
  border-color: rgba(148, 163, 184, 0.28);
  background: rgba(30, 41, 59, 0.7);
  color: #cbd5e1;
}

.library-storage--compact {
  margin-bottom: 0.65rem;
  padding: 0.45rem 0.55rem;
  border-radius: 8px;
}

.library-storage__compact-line {
  position: relative;
  display: flex;
  min-width: 0;
  align-items: center;
  gap: 0.35rem;
  color: var(--storage-muted);
  font-size: 0.68rem;
  font-weight: 650;
  line-height: 1.35;
}

.library-storage__compact-line span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dark .library-storage__compact-line { color: #99f6e4; }

@keyframes library-storage-in {
  from { opacity: 0; transform: translateY(6px); }
  to { opacity: 1; transform: translateY(0); }
}

@keyframes library-rule-in {
  from { opacity: 0; transform: translateY(4px) scale(0.98); }
  to { opacity: 1; transform: translateY(0) scale(1); }
}

@media (prefers-reduced-motion: reduce) {
  .library-storage,
  .library-storage__rule { animation: none; }
}

.library-icon-button,
.library-action {
  display: inline-grid;
  width: 2rem;
  height: 2rem;
  flex: 0 0 auto;
  place-items: center;
  border: 1px solid #d1d5db;
  border-radius: 6px;
  color: #4b5563;
  background: transparent;
}

.dark .library-icon-button,
.dark .library-action { border-color: #374151; color: #d1d5db; }
.library-icon-button:hover,
.library-action:hover { border-color: #0d9488; color: #0f766e; }
.library-action.is-danger:hover { border-color: #dc2626; color: #dc2626; }

.library-recovery { margin-bottom: 1rem; padding: 0.7rem; border: 1px solid #f59e0b; border-radius: 6px; background: #fffbeb; }
.dark .library-recovery { border-color: #92400e; background: rgba(120, 53, 15, 0.14); }
.library-recovery__heading { display: flex; align-items: flex-start; gap: 0.55rem; color: #92400e; }
.dark .library-recovery__heading { color: #fbbf24; }
.library-recovery__icon { display: grid; width: 1.7rem; height: 1.7rem; flex: 0 0 auto; place-items: center; border-radius: 5px; background: #fef3c7; }
.dark .library-recovery__icon { background: rgba(146, 64, 14, 0.35); }
.library-recovery__heading h3 { font-size: 0.76rem; font-weight: 750; }
.library-recovery__heading p { margin-top: 0.15rem; color: #a16207; font-size: 0.66rem; line-height: 1.4; }
.dark .library-recovery__heading p { color: #fcd34d; }
.library-recovery__list { margin-top: 0.55rem; border-top: 1px solid #fde68a; }
.dark .library-recovery__list { border-color: rgba(180, 83, 9, 0.45); }
.library-recovery__item { display: flex; min-width: 0; align-items: center; gap: 0.5rem; padding: 0.5rem 0; border-bottom: 1px solid #fde68a; }
.dark .library-recovery__item { border-color: rgba(180, 83, 9, 0.35); }
.library-recovery__preview { display: grid; width: 2.5rem; height: 2.5rem; flex: 0 0 auto; overflow: hidden; place-items: center; border: 1px solid #fcd34d; border-radius: 5px; color: #a16207; background: #fff; }
.dark .library-recovery__preview { border-color: #92400e; background: #111827; }
.library-recovery__preview img { width: 100%; height: 100%; object-fit: cover; }
.library-recovery__item strong,
.library-recovery__item small { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.library-recovery__item strong { color: #78350f; font-size: 0.7rem; }
.dark .library-recovery__item strong { color: #fde68a; }
.library-recovery__item small { margin-top: 0.15rem; color: #a16207; font-size: 0.62rem; }
.dark .library-recovery__item small { color: #fbbf24; }
.library-recovery__retry { display: inline-flex; min-height: 2rem; flex: 0 0 auto; align-items: center; gap: 0.3rem; padding: 0.3rem 0.55rem; border: 1px solid #f59e0b; border-radius: 5px; color: #92400e; font-size: 0.66rem; font-weight: 700; }
.dark .library-recovery__retry { color: #fcd34d; }
.library-recovery__retry:disabled { cursor: wait; opacity: 0.6; }

.library-empty {
  display: flex;
  min-height: 9rem;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.65rem;
  border: 1px dashed #d1d5db;
  border-radius: 6px;
  color: #6b7280;
  text-align: center;
  font-size: 0.8rem;
}
.dark .library-empty { border-color: #374151; color: #9ca3af; }
.library-empty--error { color: #b91c1c; }

.library-spinner { width: 1rem; height: 1rem; border: 2px solid #99f6e4; border-top-color: #0f766e; border-radius: 50%; animation: library-spin 0.7s linear infinite; }
.library-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(210px, 1fr)); gap: 0.75rem; }
.library-grid--compact { grid-template-columns: 1fr; max-height: 720px; overflow-y: auto; padding-right: 0.2rem; }

.library-item { min-width: 0; overflow: hidden; border: 1px solid #e5e7eb; border-radius: 6px; background: #fff; }
.dark .library-item { border-color: #374151; background: #111827; }
.library-item__media { position: relative; display: block; width: 100%; aspect-ratio: 4 / 3; overflow: hidden; background: #f3f4f6; }
.dark .library-item__media { background: #030712; }
.library-item__lazy { width: 100%; height: 100%; }
.library-item__media :deep(img) { width: 100%; height: 100%; object-fit: cover; }
.library-item__media:focus-visible { outline: 2px solid #0d9488; outline-offset: -2px; }
.library-item__broken { display: flex; width: 100%; height: 100%; flex-direction: column; align-items: center; justify-content: center; gap: 0.4rem; color: #9ca3af; font-size: 0.72rem; }
.library-item__mode { position: absolute; left: 0.45rem; bottom: 0.45rem; display: inline-flex; align-items: center; gap: 0.25rem; padding: 0.2rem 0.4rem; border-radius: 4px; background: rgba(17, 24, 39, 0.84); color: #f9fafb; font-size: 0.65rem; font-weight: 700; }
.library-item__body { padding: 0.65rem; }
.library-item__title-row { display: flex; min-width: 0; align-items: flex-start; gap: 0.35rem; }
.library-item__title { min-width: 0; overflow: hidden; color: #111827; font-size: 0.8rem; font-weight: 700; text-overflow: ellipsis; white-space: nowrap; }
.dark .library-item__title { color: #f9fafb; }
.library-title-edit { display: grid; width: 1.5rem; height: 1.5rem; flex: 0 0 auto; place-items: center; border-radius: 4px; color: #6b7280; }
.library-title-edit:hover,
.library-title-edit:focus-visible { color: #0f766e; background: #f0fdfa; }
.dark .library-title-edit:hover,
.dark .library-title-edit:focus-visible { color: #5eead4; background: rgba(13, 148, 136, 0.14); }
.library-title-editor { display: flex; min-width: 0; flex: 1; align-items: center; gap: 0.25rem; }
.library-title-editor .input { min-width: 0; height: 2rem; flex: 1; padding: 0.3rem 0.45rem; font-size: 0.72rem; }
.library-title-editor .library-action { width: 1.75rem; height: 1.75rem; }
.library-item__meta { margin-top: 0.25rem; overflow: hidden; color: #6b7280; font-size: 0.68rem; text-overflow: ellipsis; white-space: nowrap; }
.library-item__prompt { margin-top: 0.45rem; display: -webkit-box; overflow: hidden; color: #6b7280; font-size: 0.72rem; line-height: 1.45; -webkit-box-orient: vertical; -webkit-line-clamp: 2; }
.library-item__actions { display: flex; align-items: center; gap: 0.35rem; margin-top: 0.65rem; }
.library-text-action { min-width: 0; flex: 1; overflow: hidden; padding: 0.35rem 0.5rem; border: 1px solid #99f6e4; border-radius: 6px; color: #0f766e; font-size: 0.7rem; font-weight: 700; text-overflow: ellipsis; white-space: nowrap; }
.dark .library-text-action { border-color: rgba(45, 212, 191, 0.4); color: #5eead4; }
.library-status { flex: 0 0 auto; padding: 0.15rem 0.35rem; border-radius: 4px; background: #f3f4f6; color: #4b5563; font-size: 0.62rem; font-weight: 700; }
.dark .library-status { background: #1f2937; color: #d1d5db; }
.library-status.is-published { background: #dcfce7; color: #166534; }
.library-status.is-pending { background: #fef3c7; color: #92400e; }
.library-status.is-blocked { background: #fee2e2; color: #991b1b; }

@keyframes library-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .library-spinner { animation: none; } }
@media (max-width: 640px) { .library-grid { grid-template-columns: 1fr 1fr; } }
@media (max-width: 520px) {
  .library-recovery__item { align-items: flex-start; flex-wrap: wrap; }
  .library-recovery__item > .min-w-0 { min-width: calc(100% - 3rem); }
  .library-recovery__retry { margin-left: 3rem; }
}
@media (max-width: 420px) { .library-grid { grid-template-columns: 1fr; } }
</style>
