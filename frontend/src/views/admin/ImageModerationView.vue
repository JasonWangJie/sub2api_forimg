<template>
  <AppLayout>
    <div class="moderation-page">
      <header class="moderation-header">
        <div>
          <h1>{{ t('imageWorkflow.admin.title') }}</h1>
          <p>{{ t('imageWorkflow.admin.description') }}</p>
        </div>
        <button type="button" class="btn btn-secondary inline-flex items-center gap-2" :disabled="loading" @click="refreshActive">
          <Icon name="refresh" size="sm" :class="loading && 'animate-spin'" />
          {{ t('common.refresh') }}
        </button>
      </header>

      <nav class="moderation-tabs" :aria-label="t('imageWorkflow.admin.sections')">
        <button v-for="tab in tabs" :key="tab.value" type="button" :class="{ 'is-active': activeTab === tab.value }" @click="activeTab = tab.value">
          <Icon :name="tab.icon" size="sm" />
          {{ tab.label }}
          <span v-if="tab.count != null">{{ tab.count }}</span>
        </button>
      </nav>

      <section v-if="activeTab === 'deferred'" class="moderation-surface">
        <div class="moderation-toolbar">
          <label>
            <span>{{ t('common.status') }}</span>
            <select v-model="deferredFilter" class="input" @change="loadDeferredSubmissions()">
              <option value="pending_review">{{ t('imageWorkflow.publication.pending_review') }}</option>
              <option value="approved_pending_sync">{{ t('imageWorkflow.archive.approved_pending_sync') }}</option>
              <option value="rejected">{{ t('imageWorkflow.publication.rejected') }}</option>
              <option value="synced">{{ t('imageWorkflow.archive.synced') }}</option>
              <option value="">{{ t('common.all') }}</option>
            </select>
          </label>
        </div>
        <p class="moderation-deferred-hint">{{ t('imageWorkflow.admin.deferredHint') }}</p>
        <div class="table-scroll">
          <table class="moderation-table">
            <thead>
              <tr>
                <th>{{ t('imageWorkflow.admin.work') }}</th>
                <th>{{ t('imageWorkflow.workbench.platform') }}</th>
                <th>{{ t('imageWorkflow.admin.submitter') }}</th>
                <th>{{ t('common.status') }}</th>
                <th>{{ t('imageWorkflow.admin.submittedAt') }}</th>
                <th>{{ t('common.actions') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in deferredSubmissions" :key="item.id">
                <td>
                  <div class="work-cell">
                    <span class="work-cell__placeholder"><Icon name="inbox" size="sm" /></span>
                    <div>
                      <strong>{{ item.title || t('imageWorkflow.library.untitled') }}</strong>
                      <small>{{ item.model || '—' }} · {{ t('imageWorkflow.admin.imageHeldClientSide') }}</small>
                    </div>
                  </div>
                </td>
                <td>{{ platformName(item.platform) }}</td>
                <td>{{ item.user_id || '—' }}</td>
                <td><span class="admin-status" :class="statusClass(item.status)">{{ deferredStatusLabel(item.status) }}</span></td>
                <td>{{ formatDate(item.created_at) }}</td>
                <td>
                  <div class="table-actions">
                    <button v-if="item.status === 'pending_review'" type="button" class="action-button is-approve" :disabled="busyId === String(item.id)" @click="actDeferred(item, 'approve')">{{ t('imageWorkflow.admin.approve') }}</button>
                    <button v-if="item.status === 'pending_review'" type="button" class="action-button is-reject" :disabled="busyId === String(item.id)" @click="actDeferred(item, 'reject')">{{ t('imageWorkflow.admin.reject') }}</button>
                  </div>
                </td>
              </tr>
              <tr v-if="!deferredSubmissions.length && !loading"><td colspan="6" class="empty-cell">{{ t('common.noData') }}</td></tr>
            </tbody>
          </table>
        </div>
        <button v-if="deferredCursor" type="button" class="load-more" @click="loadMoreDeferred">{{ t('imageWorkflow.plaza.loadMore') }}</button>
      </section>

      <section v-else-if="activeTab === 'publications'" class="moderation-surface">
        <div class="moderation-toolbar">
          <label>
            <span>{{ t('common.status') }}</span>
            <select v-model="publicationFilter" class="input">
              <option value="pending_review">{{ t('imageWorkflow.publication.pending_review') }}</option>
              <option value="published">{{ t('imageWorkflow.publication.published') }}</option>
              <option value="rejected">{{ t('imageWorkflow.publication.rejected') }}</option>
              <option value="withdrawn">{{ t('imageWorkflow.publication.withdrawn') }}</option>
              <option value="admin_hidden">{{ t('imageWorkflow.publication.admin_hidden') }}</option>
              <option value="expired">{{ t('imageWorkflow.publication.expired') }}</option>
              <option value="">{{ t('common.all') }}</option>
            </select>
          </label>
          <label>
            <span>{{ t('imageWorkflow.workbench.platform') }}</span>
            <select v-model="publicationPlatform" class="input">
              <option value="">{{ t('common.all') }}</option>
              <option value="openai">OpenAI</option>
              <option value="gemini">Gemini</option>
              <option value="grok">Grok</option>
            </select>
          </label>
          <label class="moderation-id-filter">
            <span>{{ t('imageWorkflow.admin.userId') }}</span>
            <input v-model.number="publicationUserId" class="input" type="number" min="1" inputmode="numeric" :placeholder="t('imageWorkflow.admin.userIdPlaceholder')" />
          </label>
          <label class="moderation-search">
            <span>{{ t('common.search') }}</span>
            <input v-model="publicationQuery" class="input" type="search" :placeholder="t('imageWorkflow.admin.searchPublication')" @keydown.enter="loadPublications()" />
          </label>
          <button type="button" class="btn btn-secondary" data-testid="publication-filter-apply" @click="loadPublications()">
            <Icon name="search" size="sm" />
            {{ t('imageWorkflow.admin.applyFilters') }}
          </button>
          <button type="button" class="btn btn-ghost" @click="resetPublicationFilters">{{ t('imageWorkflow.admin.resetFilters') }}</button>
        </div>
        <div v-if="selectedPublicationIds.size" class="bulk-review-bar" role="status">
          <span>{{ t('imageWorkflow.admin.selectedCount', { count: selectedPublicationIds.size }) }}</span>
          <div class="table-actions">
            <button type="button" class="action-button is-approve" data-testid="bulk-approve" :disabled="bulkPublicationBusy" @click="bulkReview('approve')">
              <Icon name="check" size="xs" />
              {{ t('imageWorkflow.admin.bulkApprove') }}
            </button>
            <button type="button" class="action-button is-reject" :disabled="bulkPublicationBusy" @click="bulkReview('reject')">
              <Icon name="x" size="xs" />
              {{ t('imageWorkflow.admin.bulkReject') }}
            </button>
            <button type="button" class="action-button" :disabled="bulkPublicationBusy" @click="clearPublicationSelection">{{ t('common.cancel') }}</button>
          </div>
        </div>
        <div class="table-scroll">
          <table class="moderation-table">
            <thead><tr><th class="selection-cell"><input type="checkbox" :checked="allActionableSelected" :indeterminate.prop="someActionableSelected" :aria-label="t('imageWorkflow.admin.selectPage')" :disabled="!actionablePublications.length || bulkPublicationBusy" @change="togglePageSelection" /></th><th>{{ t('imageWorkflow.admin.work') }}</th><th>{{ t('imageWorkflow.workbench.platform') }}</th><th>{{ t('imageWorkflow.admin.submitter') }}</th><th>{{ t('common.status') }}</th><th>{{ t('imageWorkflow.admin.submittedAt') }}</th><th>{{ t('common.actions') }}</th></tr></thead>
            <tbody>
              <tr v-for="item in publications" :key="item.id">
                <td class="selection-cell"><input type="checkbox" :checked="selectedPublicationIds.has(String(item.id))" :disabled="item.status !== 'pending_review' || bulkPublicationBusy" :aria-label="t('imageWorkflow.admin.selectPublication', { title: item.title || item.id })" @change="togglePublicationSelection(item)" /></td>
                <td>
                  <div class="work-cell">
                    <img :src="item.image_url" :alt="item.title" loading="lazy" />
                    <div><strong>{{ item.title || t('imageWorkflow.library.untitled') }}</strong><small>{{ item.model || '—' }}</small></div>
                  </div>
                </td>
                <td>{{ platformName(item.platform) }}</td>
                <td>{{ item.user_label || item.public_identity || '—' }}</td>
                <td><span class="admin-status" :class="statusClass(item.status)">{{ t(`imageWorkflow.publication.${item.status}`) }}</span></td>
                <td>{{ formatDate(item.submitted_at || item.published_at) }}</td>
                <td>
                  <div class="table-actions">
                    <button v-if="item.status === 'pending_review'" type="button" class="action-button is-approve" :disabled="busyId === String(item.id)" @click="actPublication(item, 'approve')">{{ t('imageWorkflow.admin.approve') }}</button>
                    <button v-if="item.status === 'pending_review'" type="button" class="action-button is-reject" :disabled="busyId === String(item.id)" @click="actPublication(item, 'reject')">{{ t('imageWorkflow.admin.reject') }}</button>
                    <button v-if="item.status === 'published'" type="button" class="action-button is-reject" :disabled="busyId === String(item.id)" @click="actPublication(item, 'hide')">{{ t('imageWorkflow.admin.hide') }}</button>
                    <button v-if="item.status === 'admin_hidden'" type="button" class="action-button" :disabled="busyId === String(item.id)" @click="actPublication(item, 'restore')">{{ t('imageWorkflow.admin.restore') }}</button>
                    <a :href="item.image_url" target="_blank" rel="noopener" class="icon-action" :title="t('common.view')"><Icon name="externalLink" size="sm" /></a>
                  </div>
                </td>
              </tr>
              <tr v-if="!publications.length && !loading"><td colspan="7" class="empty-cell">{{ t('common.noData') }}</td></tr>
            </tbody>
          </table>
        </div>
        <button v-if="publicationCursor" type="button" class="load-more" @click="loadMorePublications">{{ t('imageWorkflow.plaza.loadMore') }}</button>
      </section>

      <section v-else-if="activeTab === 'reports'" class="moderation-surface">
        <div class="moderation-toolbar">
          <label>
            <span>{{ t('common.status') }}</span>
            <select v-model="reportFilter" class="input" @change="loadReports()">
              <option value="pending">{{ t('imageWorkflow.admin.pending') }}</option>
              <option value="resolved">{{ t('imageWorkflow.admin.resolved') }}</option>
              <option value="dismissed">{{ t('imageWorkflow.admin.dismissed') }}</option>
              <option value="">{{ t('common.all') }}</option>
            </select>
          </label>
        </div>
        <div class="report-list">
          <article v-for="report in reports" :key="report.id" class="report-row">
            <div class="report-row__main">
              <div class="flex flex-wrap items-center gap-2">
                <strong>#{{ report.id }}</strong>
                <span class="admin-status">{{ report.status }}</span>
                <span>{{ formatDate(report.created_at) }}</span>
              </div>
              <p>{{ report.reason }}<template v-if="report.detail"> · {{ report.detail }}</template></p>
              <small>{{ t('imageWorkflow.admin.publicationId') }}: {{ report.publication_id }}</small>
            </div>
            <div v-if="report.status === 'pending'" class="table-actions">
              <button type="button" class="action-button is-approve" :disabled="busyId === String(report.id)" @click="resolveReport(report, 'resolved')">{{ t('imageWorkflow.admin.resolve') }}</button>
              <button type="button" class="action-button" :disabled="busyId === String(report.id)" @click="resolveReport(report, 'dismissed')">{{ t('imageWorkflow.admin.dismiss') }}</button>
            </div>
          </article>
          <div v-if="!reports.length && !loading" class="empty-cell">{{ t('common.noData') }}</div>
        </div>
        <button v-if="reportCursor" type="button" class="load-more" @click="loadMoreReports">{{ t('imageWorkflow.plaza.loadMore') }}</button>
      </section>

      <section v-else-if="activeTab === 'library'" class="moderation-surface">
        <div class="stats-grid">
          <article v-for="stat in statsCards" :key="stat.label"><span>{{ stat.label }}</span><strong>{{ stat.value }}</strong></article>
        </div>
        <div class="moderation-toolbar moderation-toolbar--library">
          <label class="moderation-search">
            <span>{{ t('common.search') }}</span>
            <input v-model="libraryFilters.q" class="input" type="search" :placeholder="t('imageWorkflow.admin.searchLibrary')" @keydown.enter="loadLibrary()" />
          </label>
          <label class="moderation-id-filter">
            <span>{{ t('imageWorkflow.admin.userId') }}</span>
            <input v-model.number="libraryFilters.userId" class="input" type="number" min="1" inputmode="numeric" :placeholder="t('imageWorkflow.admin.userIdPlaceholder')" />
          </label>
          <label>
            <span>{{ t('imageWorkflow.workbench.platform') }}</span>
            <select v-model="libraryFilters.platform" class="input">
              <option value="">{{ t('common.all') }}</option>
              <option value="openai">OpenAI</option>
              <option value="gemini">Gemini</option>
              <option value="grok">Grok</option>
            </select>
          </label>
          <label>
            <span>{{ t('imageWorkflow.admin.source') }}</span>
            <select v-model="libraryFilters.source" class="input">
              <option value="">{{ t('common.all') }}</option>
              <option value="realtime_import">{{ t('imageWorkflow.admin.sourceRealtime') }}</option>
              <option value="async_task">{{ t('imageWorkflow.admin.sourceAsync') }}</option>
              <option value="legacy_plaza">{{ t('imageWorkflow.admin.sourceLegacy') }}</option>
            </select>
          </label>
          <label>
            <span>{{ t('imageWorkflow.admin.visibility') }}</span>
            <select v-model="libraryFilters.visibility" class="input">
              <option value="">{{ t('common.all') }}</option>
              <option value="private">{{ t('imageWorkflow.library.private') }}</option>
              <option value="public">{{ t('imageWorkflow.library.published') }}</option>
            </select>
          </label>
          <label>
            <span>{{ t('imageWorkflow.admin.publicationStatus') }}</span>
            <select v-model="libraryFilters.publicationStatus" class="input">
              <option value="">{{ t('common.all') }}</option>
              <option value="pending_review">{{ t('imageWorkflow.publication.pending_review') }}</option>
              <option value="published">{{ t('imageWorkflow.publication.published') }}</option>
              <option value="rejected">{{ t('imageWorkflow.publication.rejected') }}</option>
              <option value="withdrawn">{{ t('imageWorkflow.publication.withdrawn') }}</option>
              <option value="admin_hidden">{{ t('imageWorkflow.publication.admin_hidden') }}</option>
              <option value="expired">{{ t('imageWorkflow.publication.expired') }}</option>
            </select>
          </label>
          <button type="button" class="btn btn-secondary" data-testid="library-filter-apply" @click="loadLibrary()">
            <Icon name="search" size="sm" />
            {{ t('imageWorkflow.admin.applyFilters') }}
          </button>
          <button type="button" class="btn btn-ghost" @click="resetLibraryFilters">{{ t('imageWorkflow.admin.resetFilters') }}</button>
        </div>
        <div class="table-scroll mt-4">
          <table class="moderation-table">
            <thead><tr><th>{{ t('imageWorkflow.admin.asset') }}</th><th>{{ t('imageWorkflow.workbench.platform') }}</th><th>{{ t('imageWorkflow.admin.source') }}</th><th>{{ t('imageWorkflow.admin.storage') }}</th><th>{{ t('common.status') }}</th><th>{{ t('imageWorkflow.admin.createdAt') }}</th></tr></thead>
            <tbody>
              <tr v-for="item in libraryItems" :key="item.id">
                <td><div class="work-cell"><img v-if="adminImageURLs[String(item.id)]" :src="adminImageURLs[String(item.id)]" :alt="item.title" loading="lazy" /><span v-else class="work-cell__placeholder"><Icon name="grid" size="sm" /></span><div><strong>{{ item.title }}</strong><small>{{ item.model || '—' }}</small></div></div></td>
                <td>{{ platformName(item.platform) }}</td>
                <td>{{ item.source }}</td>
                <td>{{ formatBytes(item.byte_size || 0) }}</td>
                <td>{{ item.publication_status ? t(`imageWorkflow.publication.${item.publication_status}`) : t('imageWorkflow.library.private') }}</td>
                <td>{{ formatDate(item.created_at) }}</td>
              </tr>
              <tr v-if="!libraryItems.length && !loading"><td colspan="6" class="empty-cell">{{ t('common.noData') }}</td></tr>
            </tbody>
          </table>
        </div>
        <button v-if="libraryCursor" type="button" class="load-more" @click="loadMoreLibrary">{{ t('imageWorkflow.plaza.loadMore') }}</button>
      </section>

      <section v-else class="moderation-surface">
        <div v-if="migrationState" class="migration-status" role="status">
          <div>
            <strong>{{ t('imageWorkflow.admin.migrationTitle') }}</strong>
            <p>{{ t('imageWorkflow.admin.migrationProgress', { migrated: migrationState.migrated_count, quarantined: migrationState.quarantined_count }) }}</p>
          </div>
          <span class="admin-status" :class="statusClass(migrationState.status)">{{ t(`imageWorkflow.admin.migration.${migrationState.status}`) }}</span>
          <small v-if="migrationState.last_error">{{ migrationState.last_error }}</small>
        </div>
        <div class="cleanup-layout">
          <form class="cleanup-form" @submit.prevent="previewCleanupJob">
            <h2>{{ t('imageWorkflow.admin.cleanupPlan') }}</h2>
            <p>{{ t('imageWorkflow.admin.cleanupHint') }}</p>
            <label><span>{{ t('imageWorkflow.admin.cleanupScope') }}</span><select v-model="cleanupForm.scope" class="input"><option value="expired">{{ t('imageWorkflow.admin.expiredAssets') }}</option><option value="deleted">{{ t('imageWorkflow.admin.deletedAssets') }}</option><option value="user">{{ t('imageWorkflow.admin.userAssets') }}</option></select></label>
            <label v-if="cleanupForm.scope === 'user'"><span>{{ t('imageWorkflow.admin.userId') }}</span><input v-model.number="cleanupForm.userId" type="number" min="1" inputmode="numeric" class="input" :placeholder="t('imageWorkflow.admin.userIdPlaceholder')" /></label>
            <label v-else><span>{{ t('imageWorkflow.admin.beforeDate') }}</span><input v-model="cleanupForm.before" type="date" class="input" /></label>
            <button type="submit" class="btn btn-secondary" data-testid="cleanup-preview" :disabled="cleanupBusy || !cleanupFormValid">{{ t('imageWorkflow.admin.previewCleanup') }}</button>
            <div v-if="cleanupPreview" class="cleanup-preview" role="status">
              <strong>{{ t('imageWorkflow.admin.cleanupMatches', { count: cleanupPreview.matched_items, bytes: formatBytes(cleanupPreview.matched_bytes) }) }}</strong>
              <button type="button" class="btn btn-danger" :disabled="cleanupBusy || cleanupPreview.matched_items === 0" @click="startCleanup">{{ t('imageWorkflow.admin.executeCleanup') }}</button>
            </div>
          </form>
          <div class="cleanup-jobs">
            <h2>{{ t('imageWorkflow.admin.cleanupJobs') }}</h2>
            <article v-for="job in cleanupJobs" :key="job.id">
              <div><strong>#{{ job.id }}</strong><span class="admin-status">{{ job.status }}</span></div>
              <p>{{ cleanupScopeLabel(job.scope) }} · {{ formatCleanupFilters(job) }} · {{ formatDate(job.created_at) }}</p>
              <small>{{ t('imageWorkflow.admin.cleaned', { count: job.deleted_items || 0, bytes: formatBytes(job.deleted_bytes || 0) }) }}</small>
              <small v-if="job.error_message" class="cleanup-job-error">{{ job.error_message }}</small>
            </article>
            <div v-if="!cleanupJobs.length && !loading" class="empty-cell">{{ t('common.noData') }}</div>
          </div>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores'
import {
  createCleanupJob,
  getAdminImageLibraryStats,
  getImageLibraryMigrationState,
  listAdminImageLibrary,
  listAdminPlazaSubmissionRequests,
  listAdminPublications,
  listAdminReports,
  listCleanupJobs,
  previewCleanup,
  resolveAdminReport,
  resolveImageLibraryViewURL,
  reviewPlazaSubmissionRequest,
  reviewPublication,
} from '@/api/imageLibrary'
import type {
  ImageCleanupJob,
  ImageLibraryItem,
  ImageLibraryMigrationState,
  ImageLibraryStats,
  ImagePlazaSubmissionRequest,
  ImagePublicationRecord,
  ImageReportRecord,
} from '@/features/image-workflow/types'
import type IconComponent from '@/components/icons/Icon.vue'

type Tab = 'deferred' | 'publications' | 'reports' | 'library' | 'cleanup'
type IconName = InstanceType<typeof IconComponent>['$props']['name']
const { t } = useI18n()
const appStore = useAppStore()
const activeTab = ref<Tab>('deferred')
const loading = ref(false)
const busyId = ref('')
const deferredSubmissions = ref<ImagePlazaSubmissionRequest[]>([])
const deferredCursor = ref<string | null>(null)
const deferredFilter = ref('pending_review')
const publications = ref<ImagePublicationRecord[]>([])
const publicationCursor = ref<string | null>(null)
const publicationFilter = ref('pending_review')
const publicationQuery = ref('')
const publicationPlatform = ref('')
const publicationUserId = ref<number | ''>('')
const selectedPublicationIds = ref(new Set<string>())
const bulkPublicationBusy = ref(false)
const reports = ref<ImageReportRecord[]>([])
const reportCursor = ref<string | null>(null)
const reportFilter = ref('pending')
const libraryItems = ref<ImageLibraryItem[]>([])
const libraryCursor = ref<string | null>(null)
const stats = ref<ImageLibraryStats | null>(null)
const cleanupJobs = ref<ImageCleanupJob[]>([])
const cleanupPreview = ref<{ matched_items: number; matched_bytes: number } | null>(null)
const cleanupBusy = ref(false)
const migrationState = ref<ImageLibraryMigrationState | null>(null)
const adminImageURLs = ref<Record<string, string>>({})
const libraryFilters = reactive<{
  q: string
  userId: number | ''
  platform: string
  source: string
  visibility: string
  publicationStatus: string
}>({ q: '', userId: '', platform: '', source: '', visibility: '', publicationStatus: '' })
const cleanupForm = reactive<{ scope: string; before: string; userId: number | '' }>({
  scope: 'expired',
  before: new Date().toISOString().slice(0, 10),
  userId: '',
})

const tabs = computed<Array<{ value: Tab; label: string; icon: IconName; count?: number }>>(() => [
  { value: 'deferred', label: t('imageWorkflow.admin.deferredSubmissions'), icon: 'inbox', count: deferredFilter.value === 'pending_review' ? deferredSubmissions.value.length : undefined },
  { value: 'publications', label: t('imageWorkflow.admin.publications'), icon: 'grid', count: publicationFilter.value === 'pending_review' ? publications.value.length : undefined },
  { value: 'reports', label: t('imageWorkflow.admin.reports'), icon: 'shield', count: reportFilter.value === 'pending' ? reports.value.length : undefined },
  { value: 'library', label: t('imageWorkflow.admin.library'), icon: 'inbox' },
  { value: 'cleanup', label: t('imageWorkflow.admin.cleanup'), icon: 'trash' },
])
const statsCards = computed(() => [
  { label: t('imageWorkflow.admin.totalAssets'), value: stats.value?.object_count ?? 0 },
  { label: t('imageWorkflow.admin.totalStorage'), value: formatBytes(stats.value?.total_bytes || 0) },
  { label: t('imageWorkflow.library.private'), value: stats.value?.private_count ?? 0 },
  { label: t('imageWorkflow.library.published'), value: stats.value?.published_count ?? 0 },
])
const actionablePublications = computed(() => publications.value.filter((item) => item.status === 'pending_review'))
const allActionableSelected = computed(() => actionablePublications.value.length > 0
  && actionablePublications.value.every((item) => selectedPublicationIds.value.has(String(item.id))))
const someActionableSelected = computed(() => selectedPublicationIds.value.size > 0 && !allActionableSelected.value)
const cleanupFormValid = computed(() => cleanupForm.scope !== 'user' || Number(cleanupForm.userId) > 0)

async function loadPublications(append = false) {
  loading.value = true
  try {
    const page = await listAdminPublications({
      status: publicationFilter.value || undefined,
      platform: publicationPlatform.value || undefined,
      user_id: Number(publicationUserId.value) > 0 ? Number(publicationUserId.value) : undefined,
      q: publicationQuery.value.trim() || undefined,
      cursor: append ? publicationCursor.value || undefined : undefined,
      limit: 30,
    })
    publications.value = append ? [...publications.value, ...page.items] : page.items
    publicationCursor.value = page.next_cursor
    if (!append) clearPublicationSelection()
  } catch (cause: any) { appStore.showError(cause?.message || t('imageWorkflow.admin.loadFailed')) } finally { loading.value = false }
}
const loadMorePublications = () => loadPublications(true)

function resetPublicationFilters() {
  publicationFilter.value = 'pending_review'
  publicationPlatform.value = ''
  publicationUserId.value = ''
  publicationQuery.value = ''
  void loadPublications()
}

function clearPublicationSelection() {
  selectedPublicationIds.value = new Set()
}

function togglePublicationSelection(item: ImagePublicationRecord) {
  if (item.status !== 'pending_review') return
  const next = new Set(selectedPublicationIds.value)
  const id = String(item.id)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  selectedPublicationIds.value = next
}

function togglePageSelection() {
  if (allActionableSelected.value) {
    clearPublicationSelection()
    return
  }
  selectedPublicationIds.value = new Set(actionablePublications.value.map((item) => String(item.id)))
}

async function bulkReview(action: 'approve' | 'reject') {
  const selected = actionablePublications.value.filter((item) => selectedPublicationIds.value.has(String(item.id)))
  if (!selected.length) return
  let reason = ''
  if (action === 'reject') {
    reason = window.prompt(t('imageWorkflow.admin.bulkReasonRequired'))?.trim() || ''
    if (!reason) return
  } else if (!window.confirm(t('imageWorkflow.admin.bulkApproveConfirm', { count: selected.length }))) {
    return
  }
  bulkPublicationBusy.value = true
  try {
    const results = await Promise.allSettled(selected.map((item) => reviewPublication(item.id, action, reason)))
    const succeeded = results.filter((result) => result.status === 'fulfilled').length
    const failed = results.length - succeeded
    if (failed) appStore.showError(t('imageWorkflow.admin.bulkReviewPartial', { succeeded, failed }))
    else appStore.showSuccess(t('imageWorkflow.admin.bulkReviewComplete', { count: succeeded }))
    await loadPublications()
  } finally {
    bulkPublicationBusy.value = false
  }
}

async function actPublication(item: ImagePublicationRecord, action: 'approve' | 'reject' | 'hide' | 'restore') {
  let reason = ''
  if (action === 'reject' || action === 'hide') {
    reason = window.prompt(t('imageWorkflow.admin.reasonRequired'))?.trim() || ''
    if (!reason) return
  }
  busyId.value = String(item.id)
  try { await reviewPublication(item.id, action, reason); await loadPublications(); appStore.showSuccess(t('common.success')) }
  catch (cause: any) { appStore.showError(cause?.message || t('imageWorkflow.admin.actionFailed')) }
  finally { busyId.value = '' }
}

async function loadReports(append = false) {
  loading.value = true
  try { const page = await listAdminReports({ status: reportFilter.value || undefined, cursor: append ? reportCursor.value || undefined : undefined, limit: 30 }); reports.value = append ? [...reports.value, ...page.items] : page.items; reportCursor.value = page.next_cursor }
  catch (cause: any) { appStore.showError(cause?.message || t('imageWorkflow.admin.loadFailed')) } finally { loading.value = false }
}
const loadMoreReports = () => loadReports(true)
async function resolveReport(item: ImageReportRecord, resolution: 'resolved' | 'dismissed') {
  busyId.value = String(item.id)
  try { await resolveAdminReport(item.id, { status: resolution }); await loadReports(); appStore.showSuccess(t('common.success')) }
  catch (cause: any) { appStore.showError(cause?.message || t('imageWorkflow.admin.actionFailed')) } finally { busyId.value = '' }
}

async function loadLibrary(append = false) {
  loading.value = true
  try {
    const [page, nextStats] = await Promise.all([
      listAdminImageLibrary({
        q: libraryFilters.q.trim() || undefined,
        user_id: Number(libraryFilters.userId) > 0 ? Number(libraryFilters.userId) : undefined,
        platform: libraryFilters.platform || undefined,
        source: libraryFilters.source || undefined,
        visibility: libraryFilters.visibility || undefined,
        publication_status: libraryFilters.publicationStatus || undefined,
        cursor: append ? libraryCursor.value || undefined : undefined,
        limit: 30,
      }),
      append ? Promise.resolve(stats.value) : getAdminImageLibraryStats(),
    ])
    libraryItems.value = append ? [...libraryItems.value, ...page.items] : page.items
    libraryCursor.value = page.next_cursor
    stats.value = nextStats
    await resolveAdminImages(page.items)
  }
  catch (cause: any) { appStore.showError(cause?.message || t('imageWorkflow.admin.loadFailed')) } finally { loading.value = false }
}
const loadMoreLibrary = () => loadLibrary(true)

function resetLibraryFilters() {
  Object.assign(libraryFilters, { q: '', userId: '', platform: '', source: '', visibility: '', publicationStatus: '' })
  void loadLibrary()
}

async function loadCleanup() {
  loading.value = true
  try { const [jobs, migration] = await Promise.all([listCleanupJobs({ limit: 30 }), getImageLibraryMigrationState()]); cleanupJobs.value = jobs.items; migrationState.value = migration }
  catch (cause: any) { appStore.showError(cause?.message || t('imageWorkflow.admin.loadFailed')) } finally { loading.value = false }
}
async function previewCleanupJob() {
  cleanupBusy.value = true
  try { cleanupPreview.value = await previewCleanup(cleanupPayload()) }
  catch (cause: any) { appStore.showError(cause?.message || t('imageWorkflow.admin.actionFailed')) } finally { cleanupBusy.value = false }
}
async function startCleanup() {
  if (!cleanupPreview.value || !window.confirm(t('imageWorkflow.admin.cleanupConfirm'))) return
  cleanupBusy.value = true
  try { await createCleanupJob(cleanupPayload()); cleanupPreview.value = null; await loadCleanup(); appStore.showSuccess(t('imageWorkflow.admin.cleanupQueued')) }
  catch (cause: any) { appStore.showError(cause?.message || t('imageWorkflow.admin.actionFailed')) } finally { cleanupBusy.value = false }
}

function cleanupPayload(): Record<string, unknown> {
  if (cleanupForm.scope === 'user') return { scope: 'user', user_id: Number(cleanupForm.userId) }
  return {
    scope: cleanupForm.scope,
    before: cleanupForm.before ? new Date(`${cleanupForm.before}T23:59:59Z`).toISOString() : undefined,
  }
}

async function loadDeferredSubmissions(append = false) {
  loading.value = true
  try {
    const page = await listAdminPlazaSubmissionRequests({
      status: deferredFilter.value || undefined,
      cursor: append ? deferredCursor.value || undefined : undefined,
      limit: 30,
    })
    deferredSubmissions.value = append ? [...deferredSubmissions.value, ...page.items] : page.items
    deferredCursor.value = page.next_cursor
  } catch (cause: any) {
    appStore.showError(cause?.message || t('imageWorkflow.admin.loadFailed'))
  } finally {
    loading.value = false
  }
}
const loadMoreDeferred = () => loadDeferredSubmissions(true)

async function actDeferred(item: ImagePlazaSubmissionRequest, action: 'approve' | 'reject') {
  let reason = ''
  if (action === 'reject') {
    reason = window.prompt(t('imageWorkflow.admin.reasonRequired'))?.trim() || ''
    if (!reason) return
  }
  busyId.value = String(item.id)
  try {
    await reviewPlazaSubmissionRequest(item.id, action, reason)
    await loadDeferredSubmissions()
    appStore.showSuccess(t('common.success'))
  } catch (cause: any) {
    appStore.showError(cause?.message || t('imageWorkflow.admin.actionFailed'))
  } finally {
    busyId.value = ''
  }
}

function deferredStatusLabel(status: string) {
  if (status === 'approved_pending_sync') return t('imageWorkflow.archive.approved_pending_sync')
  if (status === 'synced') return t('imageWorkflow.archive.synced')
  if (status === 'pending_review' || status === 'rejected' || status === 'withdrawn') {
    return t(`imageWorkflow.publication.${status === 'withdrawn' ? 'withdrawn' : status}`)
  }
  return status
}

function refreshActive() {
  if (activeTab.value === 'deferred') return loadDeferredSubmissions()
  if (activeTab.value === 'publications') return loadPublications()
  if (activeTab.value === 'reports') return loadReports()
  if (activeTab.value === 'library') return loadLibrary()
  return loadCleanup()
}
function platformName(value: string) { return value === 'openai' ? 'OpenAI' : value === 'gemini' ? 'Gemini' : value === 'grok' ? 'Grok' : value }
function formatDate(value?: string | null) { if (!value) return '—'; const time = Date.parse(value); return Number.isFinite(time) ? new Date(time).toLocaleString() : value }
function formatBytes(value: number) { if (!value) return '0 B'; const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']; const index = Math.min(units.length - 1, Math.floor(Math.log(value) / Math.log(1024))); return `${(value / 1024 ** index).toFixed(index ? 1 : 0)} ${units[index]}` }
function statusClass(value: string) {
  return value === 'published' || value === 'succeeded' || value === 'synced' || value === 'approved_pending_sync'
    ? 'is-success'
    : value === 'pending_review' || value === 'pending' || value === 'running'
      ? 'is-warning'
      : value === 'rejected' || value === 'admin_hidden' || value === 'failed'
        ? 'is-danger'
        : ''
}
async function resolveAdminImages(items: ImageLibraryItem[]) { await Promise.allSettled(items.map(async (item) => { const access = await resolveImageLibraryViewURL(item.id, true); adminImageURLs.value = { ...adminImageURLs.value, [String(item.id)]: access.url } })) }

function cleanupScopeLabel(scope: string) {
  if (scope === 'expired') return t('imageWorkflow.admin.expiredAssets')
  if (scope === 'deleted') return t('imageWorkflow.admin.deletedAssets')
  if (scope === 'user') return t('imageWorkflow.admin.userAssets')
  return scope
}

function formatCleanupFilters(job: ImageCleanupJob) {
  let filters: Record<string, unknown> = {}
  if (job.filters && typeof job.filters === 'object') filters = job.filters
  else if (typeof job.filters === 'string') {
    try { filters = JSON.parse(job.filters) as Record<string, unknown> } catch { filters = {} }
  }
  if (Number(filters.user_id) > 0) return t('imageWorkflow.admin.cleanupUserFilter', { id: Number(filters.user_id) })
  if (typeof filters.before === 'string' && filters.before) return t('imageWorkflow.admin.cleanupBeforeFilter', { date: formatDate(filters.before) })
  return t('imageWorkflow.admin.cleanupEligibleFilter')
}

watch(activeTab, refreshActive)
watch(() => [cleanupForm.scope, cleanupForm.before, cleanupForm.userId], () => { cleanupPreview.value = null })
onMounted(() => { void Promise.all([loadDeferredSubmissions(), loadPublications(), loadReports()]) })
</script>

<style scoped>
.moderation-page { max-width: 1580px; margin: 0 auto; }
.moderation-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; margin-bottom: 1rem; }
.moderation-header h1 { color: #111827; font-size: 1.5rem; font-weight: 750; }
.dark .moderation-header h1 { color: #f9fafb; }
.moderation-header p { margin-top: 0.25rem; color: #6b7280; font-size: 0.85rem; }
.dark .moderation-header p { color: #9ca3af; }
.moderation-tabs { display: flex; gap: 0.3rem; overflow-x: auto; padding-bottom: 0.5rem; }
.moderation-tabs button { display: inline-flex; min-height: 2.35rem; flex: 0 0 auto; align-items: center; gap: 0.4rem; padding: 0.45rem 0.7rem; border: 1px solid #d1d5db; border-radius: 6px; color: #4b5563; font-size: 0.75rem; font-weight: 700; }
.dark .moderation-tabs button { border-color: #374151; color: #d1d5db; }
.moderation-tabs button.is-active { border-color: #0f766e; background: #f0fdfa; color: #0f766e; }
.dark .moderation-tabs button.is-active { background: rgba(13,148,136,.14); color: #5eead4; }
.moderation-tabs button span { padding: 0.05rem 0.3rem; border-radius: 4px; background: #e5e7eb; font-size: 0.62rem; }
.dark .moderation-tabs button span { background: #374151; }
.moderation-surface { padding: 0.9rem; border: 1px solid #e5e7eb; border-radius: 8px; background: #fff; }
.dark .moderation-surface { border-color: #374151; background: #111827; }
.moderation-toolbar { display: flex; flex-wrap: wrap; align-items: flex-end; gap: 0.65rem; margin-bottom: 0.8rem; }
.moderation-toolbar--library { margin-top: 1rem; }
.moderation-toolbar label > span { display: block; margin-bottom: 0.25rem; color: #6b7280; font-size: 0.65rem; font-weight: 650; }
.moderation-search { min-width: min(360px, 100%); flex: 1; }
.moderation-id-filter { width: 8.5rem; }
.moderation-toolbar .input { width: 100%; }
.moderation-toolbar .btn { display: inline-flex; min-height: 2.35rem; align-items: center; gap: 0.35rem; }
.bulk-review-bar { display: flex; min-height: 2.75rem; align-items: center; justify-content: space-between; gap: 0.75rem; margin-bottom: 0.75rem; padding: 0.5rem 0.65rem; border: 1px solid #99f6e4; border-radius: 6px; background: #f0fdfa; color: #0f766e; font-size: 0.7rem; font-weight: 700; }
.dark .bulk-review-bar { border-color: rgba(45, 212, 191, 0.35); background: rgba(13, 148, 136, 0.12); color: #5eead4; }
.table-scroll { overflow-x: auto; }
.moderation-table { width: 100%; min-width: 860px; border-collapse: collapse; font-size: 0.72rem; }
.moderation-table th { padding: 0.55rem; border-bottom: 1px solid #d1d5db; color: #6b7280; font-size: 0.64rem; font-weight: 700; text-align: left; text-transform: uppercase; }
.dark .moderation-table th { border-color: #4b5563; color: #9ca3af; }
.moderation-table td { padding: 0.6rem 0.55rem; border-bottom: 1px solid #e5e7eb; color: #4b5563; vertical-align: middle; }
.dark .moderation-table td { border-color: #1f2937; color: #d1d5db; }
.moderation-table .selection-cell { width: 2.4rem; min-width: 2.4rem; padding-right: 0.2rem; text-align: center; }
.selection-cell input { width: 1rem; height: 1rem; accent-color: #0f766e; }
.work-cell { display: flex; min-width: 220px; align-items: center; gap: 0.55rem; }
.work-cell img { width: 2.75rem; height: 2.75rem; flex: 0 0 auto; border-radius: 4px; background: #f3f4f6; object-fit: cover; }
.work-cell__placeholder { display: grid; width: 2.75rem; height: 2.75rem; flex: 0 0 auto; place-items: center; border-radius: 4px; background: #f3f4f6; color: #9ca3af; }
.moderation-deferred-hint { margin: 0 0 0.85rem; color: #6b7280; font-size: 0.875rem; }
.work-cell div { min-width: 0; }
.work-cell strong,
.work-cell small { display: block; max-width: 240px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.work-cell strong { color: #111827; font-size: 0.72rem; }
.dark .work-cell strong { color: #f9fafb; }
.work-cell small { margin-top: 0.15rem; color: #6b7280; font-size: 0.63rem; }
.admin-status { display: inline-flex; padding: 0.15rem 0.35rem; border-radius: 4px; background: #f3f4f6; color: #4b5563; font-size: 0.62rem; font-weight: 700; }
.admin-status.is-success { background: #dcfce7; color: #166534; }
.admin-status.is-warning { background: #fef3c7; color: #92400e; }
.admin-status.is-danger { background: #fee2e2; color: #991b1b; }
.table-actions { display: flex; align-items: center; gap: 0.3rem; white-space: nowrap; }
.action-button { min-height: 1.8rem; padding: 0.25rem 0.45rem; border: 1px solid #d1d5db; border-radius: 5px; color: #4b5563; font-size: 0.64rem; font-weight: 700; }
.action-button:disabled { cursor: wait; opacity: 0.55; }
.action-button.is-approve { border-color: #86efac; color: #166534; }
.action-button.is-reject { border-color: #fca5a5; color: #991b1b; }
.icon-action { display: inline-grid; width: 1.8rem; height: 1.8rem; place-items: center; border: 1px solid #d1d5db; border-radius: 5px; }
.empty-cell { padding: 2rem !important; color: #9ca3af !important; text-align: center !important; }
.load-more { width: 100%; min-height: 2.4rem; margin-top: 0.75rem; border: 1px solid #d1d5db; border-radius: 6px; color: #4b5563; font-size: 0.72rem; font-weight: 700; }
.dark .load-more { border-color: #4b5563; color: #d1d5db; }
.report-list { display: flex; flex-direction: column; }
.report-row { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: 0.75rem 0; border-bottom: 1px solid #e5e7eb; }
.dark .report-row { border-color: #374151; }
.report-row__main { min-width: 0; color: #6b7280; font-size: 0.68rem; }
.report-row__main p { margin-top: 0.35rem; overflow-wrap: anywhere; color: #374151; font-size: 0.75rem; }
.dark .report-row__main p { color: #e5e7eb; }
.report-row__main small { display: block; margin-top: 0.3rem; }
.stats-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 0.6rem; }
.stats-grid article { padding: 0.7rem; border: 1px solid #e5e7eb; border-radius: 6px; }
.dark .stats-grid article { border-color: #374151; }
.stats-grid span { color: #6b7280; font-size: 0.65rem; }
.stats-grid strong { display: block; margin-top: 0.2rem; color: #111827; font-size: 1.1rem; }
.dark .stats-grid strong { color: #f9fafb; }
.cleanup-layout { display: grid; grid-template-columns: minmax(260px, 0.8fr) minmax(320px, 1.2fr); gap: 1rem; }
.migration-status { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 0.25rem 1rem; align-items: center; margin-bottom: 0.9rem; padding: 0.75rem; border: 1px solid #d1d5db; border-radius: 6px; background: #f9fafb; }
.dark .migration-status { border-color: #374151; background: #0b1220; }
.migration-status strong { color: #111827; font-size: 0.78rem; }
.dark .migration-status strong { color: #f9fafb; }
.migration-status p,
.migration-status small { color: #6b7280; font-size: 0.68rem; }
.migration-status small { grid-column: 1 / -1; color: #991b1b; overflow-wrap: anywhere; }
.cleanup-form,
.cleanup-jobs { min-width: 0; }
.cleanup-form h2,
.cleanup-jobs h2 { font-size: 0.9rem; font-weight: 750; }
.cleanup-form > p { margin-top: 0.2rem; color: #6b7280; font-size: 0.7rem; }
.cleanup-form label { display: block; margin: 0.7rem 0; }
.cleanup-form label span { display: block; margin-bottom: 0.3rem; color: #4b5563; font-size: 0.68rem; font-weight: 650; }
.cleanup-form .input { width: 100%; }
.cleanup-preview { margin-top: 0.75rem; padding: 0.7rem; border: 1px solid #fcd34d; border-radius: 6px; background: #fffbeb; color: #92400e; }
.dark .cleanup-preview { background: rgba(146,64,14,.16); }
.cleanup-preview strong { display: block; margin-bottom: 0.6rem; font-size: 0.72rem; }
.cleanup-jobs article { padding: 0.65rem 0; border-bottom: 1px solid #e5e7eb; }
.dark .cleanup-jobs article { border-color: #374151; }
.cleanup-jobs article > div { display: flex; align-items: center; gap: 0.5rem; font-size: 0.72rem; }
.cleanup-jobs p,
.cleanup-jobs small { display: block; margin-top: 0.2rem; color: #6b7280; font-size: 0.65rem; }
.cleanup-jobs .cleanup-job-error { color: #b91c1c; overflow-wrap: anywhere; }
@media (max-width: 850px) { .stats-grid { grid-template-columns: 1fr 1fr; } .cleanup-layout { grid-template-columns: 1fr; } }
@media (max-width: 640px) { .moderation-header { flex-direction: column; } .report-row { align-items: flex-start; flex-direction: column; } .bulk-review-bar { align-items: flex-start; flex-direction: column; } .moderation-id-filter { width: 100%; } }
</style>
