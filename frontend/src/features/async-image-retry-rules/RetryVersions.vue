<template>
  <section class="card overflow-hidden" :aria-busy="loading">
    <p v-if="dirty" class="border-b border-amber-100 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-200" role="status">{{ t('retryRules.rollbackDraft') }}</p>
    <p v-if="error" class="p-4 text-sm text-red-700 dark:text-red-300" role="alert">{{ error }}</p>
    <p v-else-if="loading || !versions.length" class="p-6 text-center text-sm text-gray-500 dark:text-gray-400" role="status">{{ t(loading ? 'common.loading' : 'retryRules.noVersions') }}</p>
    <div v-else class="overflow-x-auto">
      <table class="w-full text-left text-sm">
        <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800 dark:text-gray-400"><tr><th scope="col" class="px-4 py-3">{{ t('retryRules.versions') }}</th><th scope="col" class="px-4 py-3">{{ t('retryRules.createdAt') }}</th><th scope="col" class="px-4 py-3">{{ t('retryRules.actor') }}</th><th scope="col" class="px-4 py-3">{{ t('retryRules.operation') }}</th><th scope="col" class="px-4 py-3">{{ t('common.actions') }}</th></tr></thead>
        <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
          <tr v-for="version in versions" :key="version.revision">
            <td class="whitespace-nowrap px-4 py-3 font-mono">{{ t('retryRules.revision', { revision: version.revision }) }}</td>
            <td class="whitespace-nowrap px-4 py-3">{{ new Date(version.created_at).toLocaleString(locale) }}</td>
            <td class="px-4 py-3">{{ version.actor_id || '–' }}</td>
            <td class="px-4 py-3">{{ version.rollback_from != null ? t('retryRules.rollbackFrom', { revision: version.rollback_from }) : t(`retryRules.operations.${version.operation}`, version.operation) }}</td>
            <td class="px-4 py-3">
              <div class="flex flex-wrap gap-2"><button type="button" class="btn btn-secondary btn-sm" @click="openVersion(version.revision)">{{ t('retryRules.snapshot') }}</button><button type="button" class="btn btn-secondary btn-sm" :disabled="!active || dirty || busy || active.revision === version.revision" @click="rollbackTarget = version.revision">{{ t('retryRules.rollback') }}</button></div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <Pagination v-if="total" :total="total" :page="page" :page-size="pageSize" @update:page="changePage" @update:page-size="changePageSize" />

    <BaseDialog v-if="detailRevision !== null" :show="true" :title="t('retryRules.revision', { revision: detailRevision })" width="extra-wide" @close="closeVersion">
      <p v-if="detailError" role="alert" class="text-sm text-red-700 dark:text-red-300">{{ detailError }}</p>
      <p v-else-if="!detail" role="status" class="text-sm text-gray-500 dark:text-gray-400">{{ t('common.loading') }}</p>
      <template v-else>
        <label class="mb-3 flex items-center gap-2 text-sm"><input v-model="compare" type="checkbox" class="rounded" :disabled="!active" />{{ t('retryRules.compare') }}</label>
        <div class="grid gap-4" :class="compare && active && 'md:grid-cols-2'">
          <div class="min-w-0 space-y-2">
            <h3 class="text-sm font-semibold">{{ t('retryRules.compareVersion') }}</h3>
            <pre class="max-h-[52vh] overflow-auto rounded-lg bg-gray-100 p-3 text-xs dark:bg-dark-800">{{ JSON.stringify(detail.policy, null, 2) }}</pre>
          </div>
          <div v-if="compare && active" class="min-w-0 space-y-2">
            <h3 class="text-sm font-semibold">{{ t('retryRules.compareActive') }}</h3>
            <pre class="max-h-[52vh] overflow-auto rounded-lg bg-gray-100 p-3 text-xs dark:bg-dark-800">{{ JSON.stringify(active, null, 2) }}</pre>
          </div>
        </div>
      </template>
    </BaseDialog>
    <ConfirmDialog :show="rollbackTarget !== null" :title="t('retryRules.rollback')" :message="t('retryRules.rollbackConfirm', { revision: rollbackTarget })" :confirm-text="t('retryRules.rollback')" @confirm="confirmRollback" @cancel="rollbackTarget = null" />
  </section>
</template>

<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import { asyncImageRetryRulesAPI, type RetryConfig, type RetryVersion } from '@/api/admin/asyncImageRetryRules'

const props = defineProps<{ active: RetryConfig | null; dirty: boolean; busy: boolean }>()
const emit = defineEmits<{ rollback: [targetRevision: number] }>()
const { t, locale } = useI18n()
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const versions = ref<RetryVersion[]>([])
const detail = ref<RetryVersion | null>(null)
const detailRevision = ref<number | null>(null)
const rollbackTarget = ref<number | null>(null)
const compare = ref(false)
const loading = ref(false)
const error = ref('')
const detailError = ref('')
let listController: AbortController | undefined
let detailController: AbortController | undefined

async function loadVersions() {
  listController?.abort()
  const controller = new AbortController()
  listController = controller
  loading.value = true
  error.value = ''
  try {
    const data = await asyncImageRetryRulesAPI.versions({ page: page.value, page_size: pageSize.value }, controller.signal)
    if (controller.signal.aborted) return
    versions.value = data.items || []
    total.value = data.total
  } catch {
    if (!controller.signal.aborted) error.value = t('retryRules.versionLoadFailed')
  } finally {
    if (listController === controller) loading.value = false
  }
}

function changePage(value: number) { page.value = value; void loadVersions() }
function changePageSize(value: number) { pageSize.value = value; changePage(1) }

function closeVersion() {
  detailController?.abort()
  detailRevision.value = null
  detail.value = null
  compare.value = false
}

async function openVersion(revision: number) {
  closeVersion()
  const controller = new AbortController()
  detailController = controller
  detailRevision.value = revision
  detailError.value = ''
  try {
    const version = await asyncImageRetryRulesAPI.version(revision, controller.signal)
    if (!controller.signal.aborted) detail.value = version
  } catch {
    if (!controller.signal.aborted) detailError.value = t('retryRules.versionLoadFailed')
  }
}

function confirmRollback() {
  const revision = rollbackTarget.value
  rollbackTarget.value = null
  if (revision !== null && !props.busy && !props.dirty) emit('rollback', revision)
}

watch(() => props.active?.revision, () => changePage(1), { immediate: true })
onUnmounted(() => { listController?.abort(); detailController?.abort() })
</script>
