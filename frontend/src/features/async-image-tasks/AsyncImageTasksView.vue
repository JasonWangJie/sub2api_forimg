<template>
  <AppLayout>
    <TablePageLayout>
      <template #actions>
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
            <span class="inline-flex items-center gap-1.5 rounded-md border border-gray-200 bg-white px-2.5 py-1.5 dark:border-dark-700 dark:bg-dark-800">
              <span class="h-1.5 w-1.5 rounded-full bg-amber-500"></span>
              {{ t('asyncImageTasks.summary.active', { count: activeTaskCount }) }}
            </span>
            <span class="inline-flex items-center gap-1.5 rounded-md border border-gray-200 bg-white px-2.5 py-1.5 dark:border-dark-700 dark:bg-dark-800">
              <span class="h-1.5 w-1.5 rounded-full bg-emerald-500"></span>
              {{ t('asyncImageTasks.summary.completed', { count: completedTaskCount }) }}
            </span>
            <span class="inline-flex items-center gap-1.5 rounded-md border border-gray-200 bg-white px-2.5 py-1.5 dark:border-dark-700 dark:bg-dark-800">
              <span class="h-1.5 w-1.5 rounded-full bg-rose-500"></span>
              {{ t('asyncImageTasks.summary.attention', { count: attentionTaskCount }) }}
            </span>
          </div>
          <div class="flex items-center gap-2">
            <AutoRefreshButton
              :enabled="autoRefresh.enabled.value"
              :interval-seconds="autoRefresh.intervalSeconds.value"
              :countdown="autoRefresh.countdown.value"
              :intervals="autoRefresh.intervals"
              @update:enabled="autoRefresh.setEnabled"
              @update:interval="autoRefresh.setInterval"
            />
            <button
              type="button"
              class="btn btn-secondary btn-sm inline-flex items-center gap-1.5"
              :disabled="loading"
              @click="refresh"
            >
              <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
              {{ t('common.refresh') }}
            </button>
          </div>
        </div>
      </template>

      <template #filters>
        <div class="card p-4">
          <div class="flex flex-wrap items-end justify-between gap-4">
            <div class="flex flex-1 flex-wrap items-end gap-3">
              <div class="w-full sm:min-w-[240px] sm:flex-1">
                <label class="input-label">{{ t('asyncImageTasks.filters.search') }}</label>
                <div class="relative">
                  <Icon name="search" size="sm" class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
                  <input
                    v-model.trim="filters.q"
                    class="input w-full pl-9"
                    :placeholder="t('asyncImageTasks.filters.searchPlaceholder')"
                    @keyup.enter="search"
                  />
                </div>
              </div>
              <div class="w-full sm:w-44">
                <label class="input-label">{{ t('common.status') }}</label>
                <Select v-model="filters.status" :options="statusOptions" @change="search" />
              </div>
              <div class="w-full sm:w-36">
                <label class="input-label">{{ t('asyncImageTasks.columns.platform') }}</label>
                <Select v-model="filters.platform" :options="platformOptions" @change="search" />
              </div>
              <div class="w-full sm:w-40">
                <label class="input-label">{{ t('asyncImageTasks.columns.requestType') }}</label>
                <Select v-model="filters.request_type" :options="requestTypeOptions" @change="search" />
              </div>
              <div v-if="admin" class="w-full sm:w-40">
                <label class="input-label">{{ t('asyncImageTasks.columns.storage') }}</label>
                <Select v-model="filters.storage_provider" :options="providerOptions" @change="search" />
              </div>
              <div class="w-full sm:w-36">
                <label class="input-label">{{ t('asyncImageTasks.filters.startDate') }}</label>
                <input v-model="filters.start_date" type="date" class="input w-full" />
              </div>
              <div class="w-full sm:w-36">
                <label class="input-label">{{ t('asyncImageTasks.filters.endDate') }}</label>
                <input v-model="filters.end_date" type="date" class="input w-full" />
              </div>
            </div>
            <div class="flex w-full items-center justify-end gap-2 sm:w-auto">
              <button type="button" class="btn btn-primary btn-sm" :disabled="loading" @click="search">
                {{ t('common.search') }}
              </button>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="resetFilters">
                {{ t('common.reset') }}
              </button>
            </div>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable
          :columns="columns"
          :data="tasks"
          :loading="loading"
          :row-key="taskKey"
          server-side-sort
          default-sort-key="created_at"
          default-sort-order="desc"
          :sort-storage-key="admin ? 'admin-async-image-tasks-sort' : 'user-async-image-tasks-sort'"
          @sort="sort"
        >
          <template #cell-id="{ row }">
            <div class="flex max-w-[300px] items-start gap-1.5">
              <div class="min-w-0 flex-1" :title="String(taskKey(row))">
                <span class="block truncate font-mono text-xs font-semibold text-gray-900 dark:text-gray-100">
                  {{ taskKey(row) }}
                </span>
                <span class="mt-1 block text-[11px] uppercase text-gray-400">
                  {{ protocolLabel(row.protocol) }}
                </span>
              </div>
              <button
                type="button"
                class="mt-0.5 shrink-0 rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
                :title="t('common.copy')"
                :aria-label="t('common.copy')"
                @click.stop="copyTaskId(row)"
              >
                <Icon name="copy" size="sm" />
              </button>
            </div>
          </template>

          <template #cell-created_at="{ row }">
            <div class="min-w-[190px] space-y-1 whitespace-nowrap text-xs">
              <div class="text-gray-700 dark:text-gray-200">
                <span class="mr-1 text-gray-400">{{ t('asyncImageTasks.detail.submittedAt') }}</span>
                {{ formatTime(submittedAt(row)) }}
              </div>
              <div class="text-gray-500 dark:text-gray-400">
                <span class="mr-1 text-gray-400">{{ t('asyncImageTasks.detail.startedAt') }}</span>
                {{ formatTime(row.started_at) }}
              </div>
              <div class="text-gray-500 dark:text-gray-400">
                <span class="mr-1 text-gray-400">{{ t('asyncImageTasks.detail.finishedAt') }}</span>
                {{ formatTime(row.finished_at) }}
              </div>
              <div class="font-medium text-gray-500 dark:text-gray-300">
                {{ t('asyncImageTasks.detail.duration') }}: {{ formatDuration(taskDuration(row)) }}
              </div>
            </div>
          </template>

          <template #cell-platform="{ row }">
            <div class="flex items-center gap-2">
              <span :class="platformDotClass(row.platform)" class="h-2 w-2 rounded-full"></span>
              <div>
                <div class="font-medium text-gray-800 dark:text-gray-100">{{ platformLabel(row.platform) }}</div>
                <div class="text-xs text-gray-400">{{ requestTypeLabel(row.request_type) }}</div>
              </div>
            </div>
          </template>

          <template #cell-model="{ row }">
            <div class="max-w-[240px]">
              <div class="truncate font-mono text-xs text-gray-800 dark:text-gray-200" :title="row.model">{{ row.model || '-' }}</div>
              <div class="mt-1 flex flex-wrap gap-1 text-[11px] text-gray-500 dark:text-gray-400">
                <span v-if="displaySize(row)">{{ displaySize(row) }}</span>
                <span v-if="row.aspect_ratio">{{ row.aspect_ratio }}</span>
              </div>
            </div>
          </template>

          <template #cell-status="{ row }">
            <div class="min-w-[145px]">
              <span :class="statusBadgeClass(row.status)">
                <span class="h-1.5 w-1.5 rounded-full" :class="statusDotClass(row.status)"></span>
                {{ statusLabel(row.status) }}
              </span>
              <div v-if="showProgress(row)" class="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700">
                <div
                  class="h-full rounded-full bg-primary-500 transition-[width] duration-300"
                  :style="{ width: `${normalizedProgress(row)}%` }"
                ></div>
              </div>
              <p
                v-if="row.error_message"
                class="mt-1.5 max-w-[220px] truncate text-xs text-rose-500 dark:text-rose-400"
                :title="row.error_message"
              >
                {{ row.error_message }}
              </p>
            </div>
          </template>

          <template #cell-results="{ row }">
            <div class="flex min-w-[150px] items-center gap-2.5 whitespace-nowrap">
              <button
                v-if="taskPreviewUrl(row)"
                type="button"
                class="block h-10 w-10 flex-none overflow-hidden rounded border border-gray-200 bg-gray-100 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:border-dark-700 dark:bg-dark-800"
                @click.stop="openTaskResult(row)"
              >
                <img
                  :src="taskThumbUrl(row)"
                  :alt="t('asyncImageTasks.detail.resultAlt', { index: 1 })"
                  class="h-full w-full object-cover"
                  loading="lazy"
                />
              </button>
              <div>
                <div class="text-sm font-medium text-gray-800 dark:text-gray-200">
                  {{ resultCount(row) }} / {{ row.image_count ?? resultCount(row) }}
                </div>
                <div class="mt-0.5 text-xs text-gray-400">{{ providerLabel(row.storage_provider) }}</div>
              </div>
            </div>
          </template>

          <template v-if="admin" #cell-owner="{ row }">
            <div class="max-w-[220px]">
              <div class="truncate text-sm text-gray-800 dark:text-gray-200" :title="row.user_email || ''">{{ row.user_email || '-' }}</div>
              <div class="mt-0.5 truncate text-xs text-gray-400" :title="row.api_key_name || ''">
                {{ row.api_key_name || '-' }}<span v-if="row.group_name"> · {{ row.group_name }}</span>
              </div>
            </div>
          </template>

          <template #cell-cost="{ row }">
            <span class="whitespace-nowrap font-medium tabular-nums text-emerald-600 dark:text-emerald-400">
              {{ costLabel(row.actual_cost) }}
            </span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center justify-end gap-2" @click.stop>
              <button type="button" class="inline-flex items-center gap-1 text-sm font-medium text-primary-600 hover:text-primary-700 dark:text-primary-400" @click="openDetail(row)">
                <Icon name="eye" size="sm" />
                {{ t('common.view') }}
              </button>
              <button
                v-if="admin && canResume(row)"
                type="button"
                class="inline-flex items-center gap-1 text-sm font-medium text-amber-600 hover:text-amber-700 dark:text-amber-400"
                @click="askResume(row)"
              >
                <Icon name="play" size="sm" />
                {{ t('asyncImageTasks.resume.action') }}
              </button>
            </div>
          </template>

          <template #empty>
            <div class="flex flex-col items-center py-10">
              <div class="flex h-12 w-12 items-center justify-center rounded-lg border border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-900">
                <Icon name="sparkles" size="lg" class="text-gray-400" />
              </div>
              <p class="mt-4 text-sm font-medium text-gray-600 dark:text-gray-300">{{ t('asyncImageTasks.empty.title') }}</p>
              <p class="mt-1 max-w-sm text-center text-xs leading-5 text-gray-400">{{ t('asyncImageTasks.empty.description') }}</p>
            </div>
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination
          v-if="pagination.total > 0"
          :page="pagination.page"
          :page-size="pagination.page_size"
          :total="pagination.total"
          @update:page="changePage"
          @update:pageSize="changePageSize"
        />
      </template>
    </TablePageLayout>

    <BaseDialog
      :show="detailVisible"
      :title="t('asyncImageTasks.detail.title')"
      width="extra-wide"
      :close-on-click-outside="true"
      @close="closeDetail"
    >
      <div v-if="detailLoading" class="flex min-h-64 items-center justify-center">
        <LoadingSpinner size="lg" />
      </div>
      <div v-else-if="detail" class="space-y-6 py-1">
        <section class="border-b border-gray-200 pb-5 dark:border-dark-700">
          <div class="flex flex-wrap items-start justify-between gap-4">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <span :class="statusBadgeClass(detail.status)">
                  <span class="h-1.5 w-1.5 rounded-full" :class="statusDotClass(detail.status)"></span>
                  {{ statusLabel(detail.status) }}
                </span>
                <span class="text-xs text-gray-400">{{ platformLabel(detail.platform) }} · {{ requestTypeLabel(detail.request_type) }}</span>
              </div>
              <div class="mt-3 flex items-center gap-2">
                <code class="break-all text-sm font-semibold text-gray-900 dark:text-white">{{ taskKey(detail) }}</code>
                <button type="button" class="rounded p-1 text-gray-400 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700" :title="t('common.copy')" @click="copyTaskId(detail)">
                  <Icon name="copy" size="sm" />
                </button>
              </div>
              <p v-if="detail.prompt_summary" class="mt-2 max-w-3xl text-sm leading-6 text-gray-500 dark:text-gray-400">{{ detail.prompt_summary }}</p>
            </div>
            <button
              v-if="admin && canResume(detail)"
              type="button"
              class="btn btn-secondary btn-sm inline-flex items-center gap-1.5"
              @click="askResume(detail)"
            >
              <Icon name="play" size="sm" />
              {{ t('asyncImageTasks.resume.action') }}
            </button>
          </div>

          <div class="mt-5 grid grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-3 lg:grid-cols-6">
            <DetailValue :label="t('asyncImageTasks.detail.submittedAt')" :value="formatTime(submittedAt(detail))" />
            <DetailValue :label="t('asyncImageTasks.detail.startedAt')" :value="formatTime(detail.started_at)" />
            <DetailValue :label="t('asyncImageTasks.detail.finishedAt')" :value="formatTime(detail.finished_at)" />
            <DetailValue :label="t('asyncImageTasks.detail.duration')" :value="formatDuration(taskDuration(detail))" />
            <DetailValue :label="t('asyncImageTasks.detail.actualCost')" :value="costLabel(detail.actual_cost)" />
            <DetailValue :label="t('asyncImageTasks.detail.retryCount')" :value="String(detail.retry_count ?? 0)" />
          </div>
        </section>

        <section>
          <h4 class="section-title">{{ t('asyncImageTasks.detail.request') }}</h4>
          <dl class="mt-3 grid grid-cols-1 gap-x-8 gap-y-3 sm:grid-cols-2 lg:grid-cols-4">
            <DetailRow :label="t('asyncImageTasks.columns.model')" :value="detail.model || '-'" mono />
            <DetailRow :label="t('asyncImageTasks.detail.protocol')" :value="protocolLabel(detail.protocol)" />
            <DetailRow :label="t('asyncImageTasks.detail.requestedSize')" :value="displaySize(detail) || '-'" />
            <DetailRow :label="t('asyncImageTasks.detail.aspectRatio')" :value="detail.aspect_ratio || '-'" />
            <DetailRow :label="t('asyncImageTasks.detail.imageCount')" :value="String(detail.image_count ?? resultCount(detail))" />
            <DetailRow :label="t('asyncImageTasks.detail.billingStatus')" :value="detail.billing_status || '-'" />
            <DetailRow :label="t('asyncImageTasks.detail.billingMode')" :value="detail.billing_mode || String(detail.billing_type ?? '-')" />
            <DetailRow :label="t('asyncImageTasks.detail.upstreamRequestId')" :value="detail.upstream_request_id || '-'" mono />
          </dl>
        </section>

        <section v-if="admin">
          <h4 class="section-title">{{ t('asyncImageTasks.detail.routing') }}</h4>
          <dl class="mt-3 grid grid-cols-1 gap-x-8 gap-y-3 sm:grid-cols-2 lg:grid-cols-4">
            <DetailRow :label="t('asyncImageTasks.detail.user')" :value="detail.user_email || idFallback(detail.user_id)" />
            <DetailRow :label="t('asyncImageTasks.detail.apiKey')" :value="detail.api_key_name || idFallback(detail.api_key_id)" />
            <DetailRow :label="t('asyncImageTasks.detail.group')" :value="detail.group_name || idFallback(detail.group_id)" />
            <DetailRow :label="t('asyncImageTasks.detail.account')" :value="detail.account_name || idFallback(detail.account_id)" />
          </dl>
        </section>

        <section v-if="detail.error_message" class="rounded-md border border-rose-200 bg-rose-50 p-4 dark:border-rose-900/60 dark:bg-rose-950/30">
          <div class="flex items-start gap-3">
            <Icon name="exclamationTriangle" size="sm" class="mt-0.5 flex-none text-rose-600 dark:text-rose-400" />
            <div class="min-w-0">
              <h4 class="text-sm font-semibold text-rose-800 dark:text-rose-200">{{ detail.error_code || t('asyncImageTasks.detail.error') }}</h4>
              <p class="mt-1 break-words text-sm leading-6 text-rose-700 dark:text-rose-300">{{ detail.error_message }}</p>
            </div>
          </div>
        </section>

        <section>
          <div class="flex flex-wrap items-end justify-between gap-2">
            <h4 class="section-title">{{ t('asyncImageTasks.detail.results') }}</h4>
            <span class="text-xs text-gray-400">{{ providerLabel(detail.storage_provider) }}</span>
          </div>
          <div v-if="detail.results?.length" class="mt-3 grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4">
            <figure v-for="(result, index) in detail.results" :key="result.id" class="group overflow-hidden rounded-md border border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-900">
              <button
                v-if="resultViewUrl(result)"
                type="button"
                class="block w-full aspect-square overflow-hidden bg-gray-100 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:bg-dark-800"
                @click="openResultImage(result)"
              >
                <img
                  v-if="resultThumbUrl(result)"
                  :src="resultThumbUrl(result)"
                  :alt="t('asyncImageTasks.detail.resultAlt', { index: index + 1 })"
                  class="h-full w-full object-cover transition-transform duration-200 group-hover:scale-[1.02]"
                  loading="lazy"
                />
                <span v-else class="flex h-full items-center justify-center text-gray-400">
                  <Icon name="inbox" size="lg" />
                </span>
              </button>
              <div v-else class="flex aspect-square items-center justify-center bg-gray-100 text-gray-400 dark:bg-dark-800">
                <Icon name="inbox" size="lg" />
              </div>
              <figcaption class="flex items-center justify-between gap-2 px-3 py-2 text-[11px] text-gray-500 dark:text-gray-400">
                <span>#{{ result.index ?? index + 1 }}</span>
                <span class="truncate">{{ resultSize(result) }}</span>
              </figcaption>
            </figure>
          </div>
          <div v-else class="mt-3 flex min-h-28 items-center justify-center rounded-md border border-dashed border-gray-200 text-sm text-gray-400 dark:border-dark-700">
            {{ t('asyncImageTasks.detail.resultsPending') }}
          </div>
        </section>

        <section>
          <h4 class="section-title">{{ t('asyncImageTasks.detail.timeline') }}</h4>
          <ol class="mt-4 space-y-0">
            <li v-for="(event, index) in timelineEvents" :key="event.id ?? `${event.status}-${event.created_at}-${index}`" class="relative flex gap-3 pb-5 last:pb-0">
              <span v-if="index < timelineEvents.length - 1" class="absolute left-[5px] top-3 h-full w-px bg-gray-200 dark:bg-dark-700"></span>
              <span class="relative mt-1 h-3 w-3 flex-none rounded-full border-2 border-white shadow-sm dark:border-dark-900" :class="statusDotClass(event.status)"></span>
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-baseline justify-between gap-2">
                  <span class="text-sm font-medium text-gray-800 dark:text-gray-200">{{ statusLabel(event.status) }}</span>
                  <time class="text-xs text-gray-400">{{ formatTime(event.created_at) }}</time>
                </div>
                <p v-if="event.message" class="mt-1 break-words text-xs leading-5 text-gray-500 dark:text-gray-400">{{ event.message }}</p>
              </div>
            </li>
          </ol>
        </section>
      </div>
    </BaseDialog>

    <ConfirmDialog
      :show="Boolean(resumeTarget)"
      :title="t('asyncImageTasks.resume.title')"
      :message="t('asyncImageTasks.resume.message', { id: resumeTarget ? taskKey(resumeTarget) : '' })"
      :confirm-text="t('asyncImageTasks.resume.action')"
      :cancel-text="t('common.cancel')"
      @confirm="resumeTask"
      @cancel="resumeTarget = null"
    />

    <ImageLightbox :src="lightboxSrc" :alt="lightboxAlt" @close="lightboxSrc = ''" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import AutoRefreshButton from '@/components/common/AutoRefreshButton.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import DataTable from '@/components/common/DataTable.vue'
import ImageLightbox from '@/components/common/ImageLightbox.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import type { Column } from '@/components/common/types'
import { useAutoRefresh } from '@/composables/useAutoRefresh'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatBytes, formatCurrency, formatDateTime } from '@/utils/format'
import { buildOssThumbnailUrl } from '@/utils/ossThumbnail'
import { sanitizeUrl } from '@/utils/url'

import asyncImageTasksAPI from './api'
import type {
  AsyncImageTask,
  AsyncImageTaskEvent,
  AsyncImageTaskListParams,
  AsyncImageTaskResult,
} from './types'

const props = defineProps<{ admin?: boolean }>()
const admin = computed(() => props.admin === true)
const { t, te } = useI18n()
const appStore = useAppStore()

const DetailValue = defineComponent({
  props: { label: { type: String, required: true }, value: { type: String, required: true } },
  setup(componentProps) {
    return () => h('div', { class: 'min-w-0' }, [
      h('dt', { class: 'text-[11px] font-medium uppercase text-gray-400' }, componentProps.label),
      h('dd', { class: 'mt-1 truncate text-sm font-semibold text-gray-800 dark:text-gray-100', title: componentProps.value }, componentProps.value),
    ])
  },
})

const DetailRow = defineComponent({
  props: {
    label: { type: String, required: true },
    value: { type: String, required: true },
    mono: { type: Boolean, default: false },
  },
  setup(componentProps) {
    return () => h('div', { class: 'min-w-0 border-b border-gray-100 pb-2 dark:border-dark-800' }, [
      h('dt', { class: 'text-xs text-gray-400' }, componentProps.label),
      h('dd', {
        class: ['mt-1 break-words text-sm text-gray-800 dark:text-gray-200', componentProps.mono ? 'font-mono text-xs' : 'font-medium'],
        title: componentProps.value,
      }, componentProps.value),
    ])
  },
})

const tasks = ref<AsyncImageTask[]>([])
const loading = ref(false)
const detailVisible = ref(false)
const detailLoading = ref(false)
const detail = ref<AsyncImageTask | null>(null)
const lightboxSrc = ref('')
const lightboxAlt = ref('')
const resumeTarget = ref<AsyncImageTask | null>(null)
const resuming = ref(false)
const pagination = reactive({ page: 1, page_size: 20, total: 0, pages: 0 })
const filters = reactive({
  q: '',
  status: '',
  platform: '',
  request_type: '',
  storage_provider: '',
  start_date: '',
  end_date: '',
})
const sortState = reactive({ sort_by: 'created_at', sort_order: 'desc' as 'asc' | 'desc' })
let listController: AbortController | null = null
let detailController: AbortController | null = null

const inProgressStatuses = new Set(['queued', 'invoking', 'upstream_succeeded', 'uploading', 'billing_pending'])
const attentionStatuses = new Set(['failed', 'execution_unknown', 'storage_failed', 'billing_failed', 'expired'])

const activeTaskCount = computed(() => tasks.value.filter((task) => inProgressStatuses.has(task.status)).length)
const completedTaskCount = computed(() => tasks.value.filter((task) => task.status === 'succeeded').length)
const attentionTaskCount = computed(() => tasks.value.filter((task) => attentionStatuses.has(task.status)).length)

const columns = computed<Column[]>(() => [
  { key: 'id', label: t('asyncImageTasks.columns.taskId') },
  { key: 'created_at', label: t('asyncImageTasks.columns.submittedAt'), sortable: true },
  { key: 'platform', label: t('asyncImageTasks.columns.platform') },
  { key: 'model', label: t('asyncImageTasks.columns.model') },
  { key: 'status', label: t('common.status'), sortable: true },
  { key: 'results', label: t('asyncImageTasks.columns.results') },
  ...(admin.value ? [{ key: 'owner', label: t('asyncImageTasks.columns.owner') }] : []),
  { key: 'cost', label: t('asyncImageTasks.columns.cost'), class: 'text-right' },
  { key: 'actions', label: t('common.actions'), class: 'text-right' },
])

const statusValues = [
  'queued', 'invoking', 'upstream_succeeded', 'uploading', 'billing_pending', 'succeeded',
  'failed', 'execution_unknown', 'storage_failed', 'billing_failed', 'expired',
]
const statusOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('asyncImageTasks.filters.allStatuses') },
  ...statusValues.map((value) => ({ value, label: statusLabel(value) })),
])
const platformOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('asyncImageTasks.filters.allPlatforms') },
  { value: 'gemini', label: 'Gemini' },
  { value: 'openai', label: 'OpenAI' },
])
const requestTypeOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('asyncImageTasks.filters.allRequestTypes') },
  { value: 'text_to_image', label: t('asyncImageTasks.requestType.text_to_image') },
  { value: 'image_to_image', label: t('asyncImageTasks.requestType.image_to_image') },
])
const providerOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('asyncImageTasks.filters.allProviders') },
  ...['qiniu', 'aliyun', 'tencent', 'custom_s3'].map((value) => ({ value, label: providerLabel(value) })),
])

const timelineEvents = computed<AsyncImageTaskEvent[]>(() => {
  if (!detail.value) return []
  if (detail.value.events?.length) {
    return [...detail.value.events].sort((a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime())
  }
  const events: AsyncImageTaskEvent[] = [{ status: 'queued', created_at: submittedAt(detail.value) || detail.value.created_at }]
  if (detail.value.started_at) events.push({ status: 'invoking', created_at: detail.value.started_at })
  if (detail.value.finished_at) events.push({ status: detail.value.status, created_at: detail.value.finished_at, message: detail.value.error_message })
  return events
})

const autoRefresh = useAutoRefresh({
  storageKey: props.admin ? 'admin-async-image-tasks-auto-refresh' : 'user-async-image-tasks-auto-refresh',
  intervals: [5, 10, 30, 60] as const,
  defaultInterval: 10,
  onRefresh: async () => {
    await loadTasks(true)
    if (detailVisible.value && detail.value) await loadDetail(taskKey(detail.value), true)
  },
  shouldPause: () => document.hidden || loading.value || detailLoading.value,
})

function taskKey(task: AsyncImageTask): string | number {
  return task.task_id || task.id
}

function submittedAt(task: AsyncImageTask): string {
  return task.submitted_at || task.created_at
}

function displaySize(task: AsyncImageTask): string {
  const requested = task.requested_size || task.image_size || ''
  const actual = task.actual_size || ''
  if (requested && actual && requested !== actual) return `${requested} -> ${actual}`
  return actual || requested
}

function resultCount(task: AsyncImageTask): number {
  return task.result_count ?? task.results?.length ?? 0
}

function platformLabel(value: string): string {
  if (value.toLowerCase() === 'gemini') return 'Gemini'
  if (value.toLowerCase() === 'openai') return 'OpenAI'
  return value || '-'
}

function platformDotClass(value: string): string {
  return value.toLowerCase() === 'gemini' ? 'bg-emerald-500' : 'bg-sky-500'
}

function protocolLabel(value?: string | null): string {
  if (!value) return '-'
  const normalized = value.toLowerCase()
  return normalized === 'bb' || normalized === 'sc' ? normalized.toUpperCase() : value
}

function requestTypeLabel(value: string): string {
  const key = `asyncImageTasks.requestType.${value}`
  return te(key) ? t(key) : value || '-'
}

function statusLabel(value: string): string {
  const key = `asyncImageTasks.status.${value}`
  return te(key) ? t(key) : value || '-'
}

function providerLabel(value?: string | null): string {
  if (!value) return t('asyncImageTasks.provider.pending')
  const key = `asyncImageTasks.provider.${value}`
  return te(key) ? t(key) : value
}

function statusBadgeClass(status: string): string {
  const base = 'inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs font-medium'
  if (status === 'succeeded') return `${base} border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900/70 dark:bg-emerald-950/40 dark:text-emerald-300`
  if (inProgressStatuses.has(status)) return `${base} border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900/70 dark:bg-amber-950/40 dark:text-amber-300`
  if (status === 'execution_unknown') return `${base} border-violet-200 bg-violet-50 text-violet-700 dark:border-violet-900/70 dark:bg-violet-950/40 dark:text-violet-300`
  return `${base} border-rose-200 bg-rose-50 text-rose-700 dark:border-rose-900/70 dark:bg-rose-950/40 dark:text-rose-300`
}

function statusDotClass(status: string): string {
  if (status === 'succeeded') return 'bg-emerald-500'
  if (inProgressStatuses.has(status)) return 'bg-amber-500'
  if (status === 'execution_unknown') return 'bg-violet-500'
  return 'bg-rose-500'
}

function normalizedProgress(task: AsyncImageTask): number {
  if (task.status === 'succeeded') return 100
  if (typeof task.progress === 'number' && Number.isFinite(task.progress)) {
    const value = task.progress <= 1 ? task.progress * 100 : task.progress
    return Math.min(100, Math.max(0, Math.round(value)))
  }
  const fallback: Record<string, number> = { queued: 5, invoking: 30, upstream_succeeded: 60, uploading: 75, billing_pending: 90 }
  return fallback[task.status] ?? 0
}

function showProgress(task: AsyncImageTask): boolean {
  return inProgressStatuses.has(task.status)
}

function taskDuration(task: AsyncImageTask): number | null {
  if (typeof task.duration_ms === 'number' && task.duration_ms >= 0) return task.duration_ms
  const start = new Date(task.started_at || submittedAt(task)).getTime()
  if (!Number.isFinite(start)) return null
  const end = task.finished_at ? new Date(task.finished_at).getTime() : Date.now()
  return Number.isFinite(end) && end >= start ? end - start : null
}

function formatDuration(value: number | null): string {
  if (value === null || !Number.isFinite(value)) return '-'
  if (value < 1000) return `${Math.round(value)} ms`
  if (value < 60000) return `${(value / 1000).toFixed(value < 10000 ? 1 : 0)} s`
  if (value < 3600000) return `${Math.floor(value / 60000)}m ${Math.floor((value % 60000) / 1000)}s`
  return `${Math.floor(value / 3600000)}h ${Math.floor((value % 3600000) / 60000)}m`
}

function formatTime(value?: string | null): string {
  return value ? formatDateTime(value) || '-' : '-'
}

function costLabel(value?: number | null): string {
  return typeof value === 'number' ? formatCurrency(value) : '-'
}

function safeResultUrl(value?: string | null): string {
  return sanitizeUrl(value || '', { allowRelative: true })
}

function taskPreviewUrl(task: AsyncImageTask): string {
  return safeResultUrl(task.preview_url)
}

function taskThumbUrl(task: AsyncImageTask): string {
  return buildOssThumbnailUrl(taskPreviewUrl(task), {
    width: 160,
    provider: task.storage_provider,
  })
}

function resultPreviewUrl(result: AsyncImageTaskResult): string {
  return safeResultUrl(result.preview_url)
}

function resultThumbUrl(result: AsyncImageTaskResult): string {
  return buildOssThumbnailUrl(resultPreviewUrl(result) || resultViewUrl(result), {
    width: 360,
    provider: result.provider || detail.value?.storage_provider,
  })
}

function resultViewUrl(result: AsyncImageTaskResult): string {
  return safeResultUrl(result.view_url || result.url || result.preview_url)
}

async function openResolvedImage(viewUrl?: string | null, previewUrl?: string | null): Promise<void> {
  const stableURL = safeResultUrl(viewUrl)
  const fallbackURL = safeResultUrl(previewUrl)
  if (!stableURL) {
    if (fallbackURL) {
      lightboxSrc.value = fallbackURL
      lightboxAlt.value = ''
    }
    return
  }

  try {
    const access = await asyncImageTasksAPI.resolveView(stableURL)
    const accessURL = safeResultUrl(access.url) || fallbackURL
    if (!accessURL) throw new Error('Invalid image result URL')
    lightboxSrc.value = accessURL
    lightboxAlt.value = ''
  } catch (error) {
    if (fallbackURL) {
      lightboxSrc.value = fallbackURL
      lightboxAlt.value = ''
      return
    }
    appStore.showError(extractApiErrorMessage(error, t('asyncImageTasks.errors.openResult')))
  }
}

function openTaskResult(task: AsyncImageTask): void {
  void openResolvedImage(task.view_url, task.preview_url)
}

function openResultImage(result: AsyncImageTaskResult): void {
  void openResolvedImage(result.view_url, result.preview_url || result.url)
}

function resultSize(result: AsyncImageTaskResult): string {
  const dimensions = result.width && result.height ? `${result.width}×${result.height}` : ''
  const bytes = typeof result.size_bytes === 'number' ? formatBytes(result.size_bytes) : ''
  return [dimensions, bytes].filter(Boolean).join(' · ') || providerLabel(result.provider)
}

function idFallback(value?: number | null): string {
  return value ? `#${value}` : '-'
}

function canResume(task: AsyncImageTask): boolean {
  return task.can_resume === true || ['storage_failed', 'billing_failed'].includes(task.status)
}

function buildParams(): AsyncImageTaskListParams {
  return {
    page: pagination.page,
    page_size: pagination.page_size,
    q: filters.q || undefined,
    status: filters.status || undefined,
    platform: filters.platform || undefined,
    request_type: filters.request_type || undefined,
    storage_provider: admin.value && filters.storage_provider ? filters.storage_provider : undefined,
    start_date: filters.start_date || undefined,
    end_date: filters.end_date || undefined,
    sort_by: sortState.sort_by,
    sort_order: sortState.sort_order,
  }
}

async function loadTasks(silent = false): Promise<void> {
  listController?.abort()
  const controller = new AbortController()
  listController = controller
  if (!silent) loading.value = true
  try {
    const api = admin.value ? asyncImageTasksAPI.admin : asyncImageTasksAPI.user
    const response = await api.list(buildParams(), controller.signal)
    if (controller.signal.aborted || listController !== controller) return
    tasks.value = response.items
    pagination.total = response.total
    pagination.page = response.page
    pagination.page_size = response.page_size
    pagination.pages = response.pages
    autoRefresh.resetCountdown()
  } catch (error) {
    const maybeAbort = error as { name?: string; code?: string }
    if (maybeAbort.name === 'AbortError' || maybeAbort.code === 'ERR_CANCELED') return
    appStore.showError(extractApiErrorMessage(error, t('asyncImageTasks.errors.load')))
  } finally {
    if (listController === controller) {
      listController = null
      loading.value = false
    }
  }
}

async function loadDetail(id: string | number, silent = false): Promise<void> {
  detailController?.abort()
  const controller = new AbortController()
  detailController = controller
  if (!silent) detailLoading.value = true
  try {
    const api = admin.value ? asyncImageTasksAPI.admin : asyncImageTasksAPI.user
    const response = await api.get(id, controller.signal)
    if (controller.signal.aborted || detailController !== controller) return
    detail.value = response
  } catch (error) {
    const maybeAbort = error as { name?: string; code?: string }
    if (maybeAbort.name === 'AbortError' || maybeAbort.code === 'ERR_CANCELED') return
    appStore.showError(extractApiErrorMessage(error, t('asyncImageTasks.errors.detail')))
    if (!silent) detailVisible.value = false
  } finally {
    if (detailController === controller) {
      detailController = null
      detailLoading.value = false
    }
  }
}

function openDetail(task: AsyncImageTask): void {
  detail.value = task
  detailVisible.value = true
  void loadDetail(taskKey(task))
}

function closeDetail(): void {
  detailController?.abort()
  detailController = null
  detailVisible.value = false
  detailLoading.value = false
  detail.value = null
}

function search(): void {
  pagination.page = 1
  void loadTasks()
}

function resetFilters(): void {
  Object.assign(filters, { q: '', status: '', platform: '', request_type: '', storage_provider: '', start_date: '', end_date: '' })
  pagination.page = 1
  void loadTasks()
}

function refresh(): void {
  void loadTasks()
  if (detailVisible.value && detail.value) void loadDetail(taskKey(detail.value), true)
}

function changePage(page: number): void {
  pagination.page = page
  void loadTasks()
}

function changePageSize(pageSize: number): void {
  pagination.page = 1
  pagination.page_size = pageSize
  void loadTasks()
}

function sort(key: string, order: 'asc' | 'desc'): void {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  void loadTasks()
}

async function copyTaskId(task: AsyncImageTask): Promise<void> {
  try {
    await navigator.clipboard.writeText(String(taskKey(task)))
    appStore.showSuccess(t('common.copied'))
  } catch {
    appStore.showError(t('common.copyFailed'))
  }
}

function askResume(task: AsyncImageTask): void {
  resumeTarget.value = task
}

async function resumeTask(): Promise<void> {
  if (!resumeTarget.value || resuming.value) return
  resuming.value = true
  const id = taskKey(resumeTarget.value)
  try {
    const updated = await asyncImageTasksAPI.admin.resume(id)
    appStore.showSuccess(t('asyncImageTasks.resume.success'))
    resumeTarget.value = null
    detail.value = detailVisible.value ? updated : detail.value
    await loadTasks(true)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('asyncImageTasks.errors.resume')))
  } finally {
    resuming.value = false
  }
}

onMounted(() => {
  void loadTasks()
  if (autoRefresh.enabled.value) autoRefresh.start()
})

onBeforeUnmount(() => {
  listController?.abort()
  detailController?.abort()
})
</script>

<style scoped>
.section-title {
  @apply text-xs font-semibold uppercase text-gray-500 dark:text-gray-400;
  letter-spacing: 0;
}
</style>
