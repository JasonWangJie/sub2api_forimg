<template>
  <AppLayout>
    <div class="plaza-page">
      <header class="plaza-header">
        <div>
          <h1>{{ t('imagePlaza.title') }}</h1>
          <p>{{ t('imageWorkflow.plaza.description') }}</p>
        </div>
        <div class="plaza-header__links">
          <RouterLink to="/image-library" class="btn btn-secondary inline-flex items-center gap-2">
            <Icon name="inbox" size="sm" />
            {{ t('imageWorkflow.library.title') }}
          </RouterLink>
          <RouterLink to="/image-workbench" class="btn btn-primary inline-flex items-center gap-2">
            <Icon name="bolt" size="sm" />
            {{ t('imagePlaza.goWorkbench') }}
          </RouterLink>
        </div>
      </header>

      <section class="plaza-toolbar" :aria-label="t('imageWorkflow.plaza.filters')">
        <label class="plaza-search">
          <Icon name="search" size="sm" aria-hidden="true" />
          <span class="sr-only">{{ t('imagePlaza.searchPlaceholder') }}</span>
          <input v-model="filters.q" type="search" :placeholder="t('imagePlaza.searchPlaceholder')" @keydown.enter="refresh" />
        </label>
        <label class="plaza-select">
          <span>{{ t('imageWorkflow.workbench.platform') }}</span>
          <select v-model="filters.platform" @change="refresh">
            <option value="">{{ t('common.all') }}</option>
            <option value="openai">OpenAI</option>
            <option value="gemini">Gemini</option>
            <option value="grok">Grok</option>
          </select>
        </label>
        <label class="plaza-select">
          <span>{{ t('imageWorkflow.workbench.aspectRatio') }}</span>
          <select v-model="filters.aspectRatio" @change="refresh">
            <option value="">{{ t('common.all') }}</option>
            <option v-for="ratio in ratios" :key="ratio" :value="ratio">{{ ratio }}</option>
          </select>
        </label>
        <label class="plaza-select">
          <span>{{ t('imageWorkflow.plaza.sort') }}</span>
          <select v-model="filters.sort" @change="refresh">
            <option value="newest">{{ t('imageWorkflow.plaza.newest') }}</option>
            <option value="oldest">{{ t('imageWorkflow.plaza.oldest') }}</option>
          </select>
        </label>
        <button type="button" class="plaza-icon-button" :disabled="loading" :title="t('common.refresh')" :aria-label="t('common.refresh')" @click="refresh">
          <Icon name="refresh" size="sm" :class="loading && 'animate-spin'" />
        </button>
      </section>

      <div class="plaza-summary">
        <span>{{ t('imageWorkflow.plaza.approvedOnly') }}</span>
        <strong v-if="total != null">{{ t('imagePlaza.total', { n: total }) }}</strong>
      </div>

      <div v-if="loading && !items.length" class="plaza-empty" role="status">
        <span class="plaza-spinner" aria-hidden="true"></span>
        {{ t('common.loading') }}
      </div>
      <div v-else-if="error && !items.length" class="plaza-empty is-error" role="alert">
        <Icon name="exclamationCircle" size="lg" />
        <span>{{ error }}</span>
        <button type="button" class="btn btn-secondary" @click="refresh">{{ t('common.refresh') }}</button>
      </div>
      <div v-else-if="!items.length" class="plaza-empty">
        <Icon name="grid" size="lg" />
        <strong>{{ t('imageWorkflow.plaza.empty') }}</strong>
        <span>{{ t('imageWorkflow.plaza.emptyHint') }}</span>
      </div>

      <div v-else class="plaza-grid">
        <article v-for="item in items" :key="item.id" class="plaza-item">
          <button type="button" class="plaza-item__media" @click="openPreview(item)">
            <LazyImage
              class="plaza-item__lazy"
              :src="plazaThumbUrl(item)"
              :alt="item.title || t('imageWorkflow.library.untitled')"
              @error="markBroken(item.id)"
            >
              <template #error>
                <span class="plaza-item__broken">
                  <Icon name="exclamationTriangle" size="lg" />
                  {{ t('imageWorkflow.library.imageUnavailable') }}
                </span>
              </template>
            </LazyImage>
            <span
              class="plaza-item__platform"
              :class="platformChipClass(item.platform)"
              :title="platformLabel(item.platform)"
            >
              <PlatformIcon :platform="asGroupPlatform(item.platform)" size="xs" class="plaza-item__platform-icon" />
              <span class="plaza-item__platform-name">{{ platformLabel(item.platform) }}</span>
            </span>
          </button>
          <div class="plaza-item__reuse">
            <button
              type="button"
              class="plaza-reuse-btn"
              :disabled="!item.share_prompt || !item.prompt"
              :title="item.share_prompt ? t('imagePlaza.oneClickSame') : t('imageWorkflow.plaza.promptPrivate')"
              @click="reuse(item)"
            >
              <Icon name="sparkles" size="sm" />
              {{ t('imagePlaza.oneClickSame') }}
            </button>
          </div>
          <div class="plaza-item__body">
            <div class="plaza-item__title-row">
              <h2 :title="item.title">{{ item.title || t('imageWorkflow.library.untitled') }}</h2>
              <span v-if="item.is_owner" class="owner-badge">{{ t('imageWorkflow.plaza.mine') }}</span>
            </div>
            <p class="plaza-item__meta">
              <span>{{ item.model || '—' }}</span>
              <span>{{ item.aspect_ratio || item.size || '—' }}</span>
              <span>{{ formatDate(item.published_at) }}</span>
            </p>
            <p
              v-if="item.share_prompt && item.prompt"
              class="plaza-item__prompt"
              :title="item.prompt"
            >{{ item.prompt }}</p>
            <p v-else class="plaza-item__prompt is-private">{{ t('imageWorkflow.plaza.promptPrivate') }}</p>
            <div class="plaza-item__publisher">
              <Icon name="userCircle" size="sm" />
              <span>{{ item.public_identity }}</span>
            </div>
          </div>
          <div class="plaza-item__actions">
            <button
              type="button"
              class="plaza-text-action"
              :title="t('imageWorkbench.view')"
              @click="openPreview(item)"
            >
              <Icon name="eye" size="sm" />
              {{ t('batchImage.detail.preview') }}
            </button>
            <a
              class="plaza-text-action"
              :href="resolvePlazaImageUrl(item)"
              target="_blank"
              rel="noopener"
              :title="t('imageWorkbench.download')"
            >
              <Icon name="download" size="sm" />
              {{ t('imageWorkbench.download') }}
            </a>
            <button
              v-if="authStore.isAdmin"
              type="button"
              class="plaza-text-action is-danger"
              :disabled="busyId === String(item.id)"
              @click="adminHide(item)"
            >
              <Icon name="trash" size="sm" />
              {{ t('common.delete') }}
            </button>
            <button
              v-else
              type="button"
              class="plaza-text-action"
              @click="openReport(item)"
            >
              <Icon name="exclamationTriangle" size="sm" />
              {{ t('imageWorkflow.plaza.report') }}
            </button>
          </div>
        </article>
      </div>

      <button
        v-if="nextCursor"
        ref="loadMoreSentinel"
        type="button"
        class="plaza-load-more"
        :disabled="loadingMore"
        @click="loadMore"
      >
        <span v-if="loadingMore" class="plaza-spinner" aria-hidden="true"></span>
        {{ t('imageWorkflow.plaza.loadMore') }}
      </button>

      <Teleport to="body">
        <div
          v-if="previewItem"
          class="plaza-lightbox"
          role="dialog"
          aria-modal="true"
          :aria-label="previewItem.title || t('imageWorkflow.library.untitled')"
          @click="closePreview"
        >
          <button type="button" class="plaza-lightbox__close" :title="t('common.close')" :aria-label="t('common.close')" @click="closePreview">
            <Icon name="x" size="sm" />
          </button>
          <img
            class="plaza-lightbox__img"
            :src="resolvePlazaImageUrl(previewItem)"
            :alt="previewItem.title || t('imageWorkflow.library.untitled')"
            @click.stop
          />
        </div>
      </Teleport>

      <dialog ref="reportDialog" class="plaza-dialog" @click="closeReportOnBackdrop">
        <form class="plaza-dialog__surface plaza-dialog__surface--form" @submit.prevent="submitReport">
          <header>
            <div>
              <h2>{{ t('imageWorkflow.plaza.reportTitle') }}</h2>
              <p>{{ reportItem?.title }}</p>
            </div>
            <button type="button" class="plaza-icon-button" :title="t('common.close')" :aria-label="t('common.close')" @click="closeReport">
              <Icon name="x" size="sm" />
            </button>
          </header>
          <label class="dialog-field">
            <span>{{ t('imageWorkflow.plaza.reportReason') }}</span>
            <select v-model="reportForm.reason" class="input" required>
              <option v-for="reason in IMAGE_PLAZA_REPORT_REASONS" :key="reason" :value="reason">
                {{ t(`imageWorkflow.plaza.reasons.${reason}`) }}
              </option>
            </select>
          </label>
          <label class="dialog-field">
            <span>{{ t('imageWorkflow.plaza.reportDetail') }}</span>
            <textarea v-model="reportForm.detail" class="input" rows="4" maxlength="500"></textarea>
          </label>
          <footer>
            <button type="button" class="btn btn-secondary" @click="closeReport">{{ t('common.cancel') }}</button>
            <button type="submit" class="btn btn-danger" :disabled="reporting">{{ t('imageWorkflow.plaza.submitReport') }}</button>
          </footer>
        </form>
      </dialog>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import LazyImage from '@/components/common/LazyImage.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import { useInView } from '@/composables/useInView'
import {
  IMAGE_PLAZA_REPORT_REASONS,
  listImagePlaza,
  reportImagePlaza,
  resolvePlazaImageUrl,
  type ImagePlazaReportReason,
} from '@/api/imagePlaza'
import { reviewPublication } from '@/api/imageLibrary'
import type { ImagePlazaItem } from '@/features/image-workflow/types'
import { useAppStore, useAuthStore } from '@/stores'
import type { GroupPlatform } from '@/types'
import { platformLabel } from '@/utils/platformColors'
import { buildOssThumbnailUrl } from '@/utils/ossThumbnail'

const { t } = useI18n()
const router = useRouter()
const appStore = useAppStore()
const authStore = useAuthStore()
const ratios = ['1:1', '2:3', '3:2', '3:4', '4:3', '4:5', '5:4', '9:16', '16:9', '21:9']
const loading = ref(false)
const loadingMore = ref(false)
const reporting = ref(false)
const busyId = ref('')
const error = ref('')
const items = ref<ImagePlazaItem[]>([])
const nextCursor = ref<string | null>(null)
const total = ref<number | null>(null)
const broken = ref(new Set<string>())
const reportDialog = ref<HTMLDialogElement | null>(null)
const previewItem = ref<ImagePlazaItem | null>(null)
const reportItem = ref<ImagePlazaItem | null>(null)
const { target: loadMoreSentinel, inView: loadMoreInView } = useInView({ rootMargin: '400px 0px', once: false })
const filters = reactive({ q: '', platform: '', aspectRatio: '', sort: 'newest' as 'newest' | 'oldest' })
const reportForm = reactive<{ reason: ImagePlazaReportReason; detail: string }>({
  reason: IMAGE_PLAZA_REPORT_REASONS[0],
  detail: '',
})

function query(cursor?: string) {
  return {
    q: filters.q.trim() || undefined,
    platform: filters.platform || undefined,
    aspect_ratio: filters.aspectRatio || undefined,
    sort: filters.sort,
    cursor,
    limit: 9,
  }
}

async function refresh() {
  loading.value = true
  error.value = ''
  try {
    const page = await listImagePlaza(query())
    items.value = page.items
    nextCursor.value = page.next_cursor
    total.value = page.total ?? null
  } catch (cause: any) {
    error.value = cause?.message || t('imagePlaza.loadFailed')
  } finally {
    loading.value = false
  }
}

async function loadMore() {
  if (!nextCursor.value || loadingMore.value) return
  loadingMore.value = true
  try {
    const page = await listImagePlaza(query(nextCursor.value))
    const existing = new Set(items.value.map((item) => String(item.id)))
    items.value.push(...page.items.filter((item) => !existing.has(String(item.id))))
    nextCursor.value = page.next_cursor
  } catch (cause: any) {
    appStore.showError(cause?.message || t('imagePlaza.loadFailed'))
  } finally {
    loadingMore.value = false
    await nextTick()
    if (loadMoreInView.value && nextCursor.value) void loadMore()
  }
}

function plazaThumbUrl(item: ImagePlazaItem) {
  if (broken.value.has(String(item.id))) return ''
  return buildOssThumbnailUrl(resolvePlazaImageUrl(item), { width: 480 })
}

const REUSE_PROMPT_KEY = 'image-workbench:reuse-payload'

function reuse(item: ImagePlazaItem) {
  if (!item.share_prompt || !item.prompt) return
  closePreview()
  const payload = {
    prompt: item.prompt,
    model: item.model || undefined,
    size: item.size || undefined,
  }
  // Prefer sessionStorage so the full prompt is kept (URL length can truncate long text).
  let stored = false
  try {
    sessionStorage.setItem(REUSE_PROMPT_KEY, JSON.stringify(payload))
    stored = true
  } catch {
    stored = false
  }
  router.push({
    path: '/image-workbench',
    query: stored
      ? { reuse: '1' }
      : {
          prompt: payload.prompt,
          model: payload.model,
          size: payload.size,
        },
  })
}

async function adminHide(item: ImagePlazaItem) {
  const id = item.publication_id || item.id
  if (!id || !window.confirm(t('imageWorkflow.plaza.adminDeleteConfirm'))) return
  busyId.value = String(item.id)
  try {
    await reviewPublication(id, 'hide', t('imageWorkflow.plaza.adminDeleteReason'))
    items.value = items.value.filter((candidate) => candidate.id !== item.id)
    if (total.value != null) total.value = Math.max(0, total.value - 1)
    appStore.showSuccess(t('imageWorkflow.plaza.adminDeleted'))
  } catch (cause: any) {
    appStore.showError(cause?.message || t('imageWorkflow.library.actionFailed'))
  } finally {
    busyId.value = ''
  }
}

function lockBodyScroll(locked: boolean) {
  document.body.style.overflow = locked ? 'hidden' : ''
}

function onPreviewKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && previewItem.value) closePreview()
}

function openPreview(item: ImagePlazaItem) {
  previewItem.value = item
  lockBodyScroll(true)
}

function closePreview() {
  previewItem.value = null
  lockBodyScroll(false)
}

function openReport(item: ImagePlazaItem) {
  reportItem.value = item
  reportForm.reason = IMAGE_PLAZA_REPORT_REASONS[0]
  reportForm.detail = ''
  reportDialog.value?.showModal()
}
function closeReport() { reportDialog.value?.close(); reportItem.value = null }
function closeReportOnBackdrop(event: MouseEvent) { if (event.target === reportDialog.value) closeReport() }

async function submitReport() {
  if (!reportItem.value) return
  reporting.value = true
  try {
    await reportImagePlaza(reportItem.value.publication_id || reportItem.value.id, {
      reason: reportForm.reason,
      detail: reportForm.detail.trim() || undefined,
    })
    closeReport()
    appStore.showSuccess(t('imageWorkflow.plaza.reportSubmitted'))
  } catch (cause: any) {
    appStore.showError(cause?.message || t('imageWorkflow.plaza.reportFailed'))
  } finally {
    reporting.value = false
  }
}

function markBroken(id: string | number) { broken.value = new Set([...broken.value, String(id)]) }
function asGroupPlatform(platform: string): GroupPlatform | undefined {
  if (platform === 'openai' || platform === 'gemini' || platform === 'grok' || platform === 'anthropic' || platform === 'antigravity') {
    return platform
  }
  return undefined
}
function platformChipClass(platform: string) {
  if (platform === 'openai') return 'is-openai'
  if (platform === 'gemini') return 'is-gemini'
  if (platform === 'grok') return 'is-grok'
  return 'is-default'
}
function formatDate(value: string) { const time = Date.parse(value); return Number.isFinite(time) ? new Date(time).toLocaleString() : value }

watch(loadMoreInView, (visible) => {
  if (visible && nextCursor.value && !loadingMore.value && !loading.value) void loadMore()
})

onMounted(() => {
  window.addEventListener('keydown', onPreviewKeydown)
  void refresh()
})

onUnmounted(() => {
  window.removeEventListener('keydown', onPreviewKeydown)
  lockBodyScroll(false)
})
</script>

<style scoped>
.plaza-page { max-width: 1580px; margin: 0 auto; }
.plaza-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; margin-bottom: 1rem; }
.plaza-header h1 { color: #111827; font-size: 1.5rem; font-weight: 750; line-height: 1.25; }
.dark .plaza-header h1 { color: #f9fafb; }
.plaza-header p { margin-top: 0.3rem; color: #6b7280; font-size: 0.875rem; }
.dark .plaza-header p { color: #9ca3af; }
.plaza-header__links { display: flex; flex-wrap: wrap; gap: 0.5rem; }
.plaza-toolbar { display: grid; grid-template-columns: minmax(220px, 1fr) repeat(3, minmax(120px, auto)) 2.25rem; align-items: end; gap: 0.5rem; padding: 0.65rem; border: 1px solid #e5e7eb; border-radius: 8px; background: #fff; }
.dark .plaza-toolbar { border-color: #374151; background: #111827; }
.plaza-search { display: flex; height: 2.25rem; align-items: center; gap: 0.45rem; padding: 0 0.65rem; border: 1px solid #d1d5db; border-radius: 6px; color: #6b7280; }
.dark .plaza-search { border-color: #4b5563; }
.plaza-search:focus-within { border-color: #0d9488; box-shadow: 0 0 0 2px rgba(13,148,136,0.14); }
.plaza-search input { min-width: 0; flex: 1; border: 0; outline: 0; background: transparent; color: #111827; font-size: 0.78rem; }
.dark .plaza-search input { color: #f3f4f6; }
.plaza-select span { display: block; margin-bottom: 0.25rem; color: #6b7280; font-size: 0.63rem; font-weight: 650; }
.plaza-select select { width: 100%; height: 2.25rem; padding: 0 1.65rem 0 0.55rem; border: 1px solid #d1d5db; border-radius: 6px; background: transparent; color: inherit; font-size: 0.72rem; }
.dark .plaza-select select { border-color: #4b5563; }
.plaza-icon-button,
.plaza-action { display: inline-grid; width: 2.25rem; height: 2.25rem; flex: 0 0 auto; place-items: center; border: 1px solid #d1d5db; border-radius: 6px; color: #4b5563; }
.dark .plaza-icon-button,
.dark .plaza-action { border-color: #4b5563; color: #d1d5db; }
.plaza-icon-button:hover,
.plaza-action:hover { border-color: #0d9488; color: #0f766e; }
.plaza-action:disabled { cursor: not-allowed; opacity: 0.35; }
.plaza-summary { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: 0.65rem 0.15rem; color: #6b7280; font-size: 0.7rem; }
.plaza-summary strong { color: #0f766e; font-weight: 700; }

.plaza-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 0.75rem; }
.plaza-item { display: flex; min-width: 0; flex-direction: column; overflow: hidden; border: 1px solid #e5e7eb; border-radius: 8px; background: #fff; }
.dark .plaza-item { border-color: #374151; background: #111827; }
.plaza-item__media { position: relative; display: block; width: 100%; aspect-ratio: 1 / 1; overflow: hidden; background: #f3f4f6; }
.dark .plaza-item__media { background: #030712; }
.plaza-item__lazy { width: 100%; height: 100%; }
.plaza-item__media :deep(img) { width: 100%; height: 100%; object-fit: cover; transition: transform 0.18s ease; }
.plaza-item__media:hover :deep(img) { transform: scale(1.015); }
.plaza-item__media:focus-visible { outline: 2px solid #0d9488; outline-offset: -2px; }
.plaza-item__platform {
  position: absolute;
  left: 0.5rem;
  bottom: 0.5rem;
  z-index: 1;
  display: inline-flex;
  max-width: calc(100% - 1rem);
  align-items: center;
  gap: 0.32rem;
  padding: 0.28rem 0.58rem 0.28rem 0.38rem;
  overflow: hidden;
  border: 1px solid rgba(255, 255, 255, 0.18);
  border-radius: 999px;
  background: rgba(17, 24, 39, 0.58);
  box-shadow:
    0 1px 0 rgba(255, 255, 255, 0.12) inset,
    0 6px 16px rgba(0, 0, 0, 0.22);
  color: #f8fafc;
  font-size: 0.62rem;
  font-weight: 700;
  letter-spacing: 0.03em;
  line-height: 1;
  backdrop-filter: blur(12px) saturate(1.25);
  -webkit-backdrop-filter: blur(12px) saturate(1.25);
  pointer-events: none;
}
.plaza-item__platform-icon,
.plaza-item__platform :deep(svg) {
  width: 0.72rem;
  height: 0.72rem;
  flex: 0 0 auto;
  opacity: 0.95;
}
.plaza-item__platform-name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.plaza-item__platform.is-openai {
  border-color: rgba(52, 211, 153, 0.42);
  background:
    linear-gradient(135deg, rgba(6, 95, 70, 0.78), rgba(17, 24, 39, 0.42)),
    rgba(17, 24, 39, 0.45);
  color: #a7f3d0;
  box-shadow:
    0 1px 0 rgba(167, 243, 208, 0.18) inset,
    0 6px 16px rgba(0, 0, 0, 0.22),
    0 0 0 1px rgba(16, 185, 129, 0.08);
}
.plaza-item__platform.is-gemini {
  border-color: rgba(96, 165, 250, 0.45);
  background:
    linear-gradient(135deg, rgba(30, 64, 175, 0.78), rgba(17, 24, 39, 0.42)),
    rgba(17, 24, 39, 0.45);
  color: #bfdbfe;
  box-shadow:
    0 1px 0 rgba(191, 219, 254, 0.18) inset,
    0 6px 16px rgba(0, 0, 0, 0.22),
    0 0 0 1px rgba(59, 130, 246, 0.08);
}
.plaza-item__platform.is-grok {
  border-color: rgba(212, 212, 216, 0.35);
  background:
    linear-gradient(135deg, rgba(39, 39, 42, 0.88), rgba(17, 24, 39, 0.48)),
    rgba(17, 24, 39, 0.5);
  color: #e4e4e7;
  box-shadow:
    0 1px 0 rgba(244, 244, 245, 0.12) inset,
    0 6px 16px rgba(0, 0, 0, 0.24);
}
.plaza-item__platform.is-default {
  color: #e2e8f0;
}
.plaza-item__broken { display: flex; width: 100%; height: 100%; flex-direction: column; align-items: center; justify-content: center; gap: 0.35rem; color: #9ca3af; font-size: 0.7rem; }
.plaza-item__reuse { padding: 0.55rem 0.7rem 0; }
.plaza-reuse-btn {
  display: inline-flex;
  width: 100%;
  height: 2.15rem;
  align-items: center;
  justify-content: center;
  gap: 0.35rem;
  border: 1px solid #99f6e4;
  border-radius: 6px;
  background: #f0fdfa;
  color: #0f766e;
  font-size: 0.74rem;
  font-weight: 700;
}
.plaza-reuse-btn:hover:not(:disabled) { border-color: #14b8a6; background: #ccfbf1; color: #115e59; }
.plaza-reuse-btn:disabled { cursor: not-allowed; opacity: 0.4; }
.dark .plaza-reuse-btn { border-color: #134e4a; background: rgba(13,148,136,0.16); color: #5eead4; }
.dark .plaza-reuse-btn:hover:not(:disabled) { border-color: #2dd4bf; background: rgba(13,148,136,0.28); color: #99f6e4; }
.plaza-item__body { padding: 0.7rem 0.7rem 0.4rem; }
.plaza-item__title-row { display: flex; min-width: 0; align-items: flex-start; gap: 0.45rem; }
.plaza-item__title-row h2 { min-width: 0; flex: 1; overflow: hidden; color: #111827; font-size: 0.82rem; font-weight: 750; text-overflow: ellipsis; white-space: nowrap; }
.dark .plaza-item__title-row h2 { color: #f9fafb; }
.owner-badge { flex: 0 0 auto; padding: 0.12rem 0.32rem; border-radius: 4px; background: #ccfbf1; color: #115e59; font-size: 0.6rem; font-weight: 700; }
.plaza-item__meta { display: flex; flex-wrap: wrap; gap: 0.35rem 0.6rem; margin-top: 0.3rem; color: #6b7280; font-size: 0.64rem; }
.plaza-item__prompt {
  display: -webkit-box;
  min-height: 3.15rem;
  margin-top: 0.45rem;
  overflow: hidden;
  color: #4b5563;
  font-size: 0.7rem;
  line-height: 1.5;
  text-overflow: ellipsis;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
  line-clamp: 3;
  word-break: break-word;
}
.dark .plaza-item__prompt { color: #d1d5db; }
.plaza-item__prompt.is-private { color: #9ca3af; font-style: italic; }
.plaza-item__publisher { display: flex; min-width: 0; align-items: center; gap: 0.3rem; margin-top: 0.55rem; color: #6b7280; font-size: 0.66rem; }
.plaza-item__publisher span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.plaza-item__actions { display: flex; align-items: center; gap: 0.35rem; margin-top: auto; padding: 0.4rem 0.7rem 0.7rem; }
.plaza-text-action {
  display: inline-flex;
  min-width: 0;
  flex: 1;
  height: 2.25rem;
  align-items: center;
  justify-content: center;
  gap: 0.28rem;
  overflow: hidden;
  padding: 0 0.4rem;
  border: 1px solid #d1d5db;
  border-radius: 6px;
  color: #4b5563;
  font-size: 0.68rem;
  font-weight: 700;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dark .plaza-text-action { border-color: #4b5563; color: #d1d5db; }
.plaza-text-action:hover { border-color: #0d9488; color: #0f766e; }
.plaza-text-action.is-danger { border-color: #fca5a5; color: #b91c1c; }
.plaza-text-action.is-danger:hover { border-color: #ef4444; color: #991b1b; }
.plaza-empty { display: flex; min-height: 17rem; flex-direction: column; align-items: center; justify-content: center; gap: 0.45rem; border: 1px dashed #d1d5db; border-radius: 8px; color: #6b7280; text-align: center; font-size: 0.78rem; }
.dark .plaza-empty { border-color: #374151; color: #9ca3af; }
.plaza-empty.is-error { color: #b91c1c; }
.plaza-spinner { display: inline-block; width: 1rem; height: 1rem; border: 2px solid #99f6e4; border-top-color: #0f766e; border-radius: 50%; animation: plaza-spin 0.75s linear infinite; }
.plaza-load-more { display: flex; width: 100%; min-height: 2.6rem; align-items: center; justify-content: center; gap: 0.45rem; margin-top: 0.9rem; border: 1px solid #d1d5db; border-radius: 6px; color: #4b5563; font-size: 0.78rem; font-weight: 700; }
.dark .plaza-load-more { border-color: #4b5563; color: #d1d5db; }

.plaza-lightbox {
  position: fixed;
  inset: 0;
  z-index: 80;
  display: grid;
  place-items: center;
  padding: 1.25rem;
  background: rgba(3, 7, 18, 0.88);
  cursor: zoom-out;
}
.plaza-lightbox__img {
  max-width: min(96vw, 1400px);
  max-height: 92vh;
  width: auto;
  height: auto;
  object-fit: contain;
  cursor: default;
  box-shadow: 0 18px 48px rgba(0, 0, 0, 0.45);
}
.plaza-lightbox__close {
  position: fixed;
  top: 1rem;
  right: 1rem;
  z-index: 81;
  display: inline-grid;
  width: 2.5rem;
  height: 2.5rem;
  place-items: center;
  border: 1px solid rgba(255, 255, 255, 0.28);
  border-radius: 999px;
  background: rgba(17, 24, 39, 0.72);
  color: #f9fafb;
  cursor: pointer;
}
.plaza-lightbox__close:hover { background: rgba(17, 24, 39, 0.92); border-color: rgba(255, 255, 255, 0.5); }

.plaza-dialog { width: min(880px, calc(100vw - 2rem)); max-height: calc(100vh - 2rem); padding: 0; overflow: auto; border: 0; border-radius: 8px; background: transparent; }
.plaza-dialog::backdrop { background: rgba(3,7,18,0.72); }
.plaza-dialog__surface { overflow: hidden; border: 1px solid #d1d5db; border-radius: 8px; background: #fff; color: #111827; }
.dark .plaza-dialog__surface { border-color: #374151; background: #111827; color: #f9fafb; }
.plaza-dialog__surface > header { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; padding: 0.8rem; border-bottom: 1px solid #e5e7eb; }
.dark .plaza-dialog__surface > header { border-color: #374151; }
.plaza-dialog__surface > header h2 { overflow-wrap: anywhere; font-size: 0.95rem; font-weight: 750; }
.plaza-dialog__surface > header p { margin-top: 0.15rem; color: #6b7280; font-size: 0.68rem; }
.plaza-dialog__surface > footer { display: flex; justify-content: flex-end; gap: 0.5rem; padding: 0.8rem; border-top: 1px solid #e5e7eb; }
.dark .plaza-dialog__surface > footer { border-color: #374151; }
.plaza-dialog__surface--form { padding-bottom: 0.1rem; }
.dialog-field { display: block; margin: 0.8rem; }
.dialog-field > span { display: block; margin-bottom: 0.35rem; color: #4b5563; font-size: 0.72rem; font-weight: 650; }
.dark .dialog-field > span { color: #d1d5db; }
.dialog-field .input { width: 100%; }

@keyframes plaza-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .plaza-spinner { animation: none; } .plaza-item__media :deep(img) { transition: none; } }
@media (max-width: 900px) { .plaza-toolbar { grid-template-columns: 1fr 1fr; } .plaza-search { grid-column: 1 / -1; } .plaza-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 640px) { .plaza-header { flex-direction: column; } .plaza-toolbar { grid-template-columns: 1fr; } .plaza-search { grid-column: auto; } .plaza-grid { grid-template-columns: 1fr; } .plaza-text-action { font-size: 0.62rem; } }
@media (max-width: 430px) { .plaza-header__links { width: 100%; } .plaza-header__links > * { flex: 1; justify-content: center; } }
</style>
