<template>
  <section class="card" data-testid="image-resolution-settings" :aria-busy="busy">
    <div class="flex flex-col gap-3 border-b border-gray-100 px-6 py-4 sm:flex-row sm:items-start sm:justify-between dark:border-dark-700">
      <div>
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('admin.settings.imageResolutionGuard.title') }}</h2>
        <p class="mt-1 text-sm leading-6 text-gray-500 dark:text-gray-400">{{ t('admin.settings.imageResolutionGuard.description') }}</p>
      </div>
      <span class="inline-flex shrink-0 items-center self-start rounded-md px-2 py-1 text-xs font-medium" :class="form.enabled ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'">
        {{ t(form.enabled ? 'admin.settings.imageResolutionGuard.enabledStatus' : 'admin.settings.imageResolutionGuard.disabledStatus') }}
      </span>
    </div>
    <div class="space-y-5 p-6">
      <p v-if="loading" role="status" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
      <div v-if="error" role="alert" class="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300">{{ error }}</div>
      <fieldset :disabled="busy || !loaded" class="min-w-0 space-y-5 disabled:opacity-60">
        <div class="flex items-start justify-between gap-4">
          <div>
            <label id="resolution-guard-enabled-label" class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.settings.imageResolutionGuard.enabled') }}</label>
            <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.settings.imageResolutionGuard.enabledHint') }}</p>
          </div>
          <Toggle v-model="form.enabled" :disabled="busy || !loaded" aria-labelledby="resolution-guard-enabled-label" data-testid="guard-enabled" />
        </div>
        <div class="grid gap-4 sm:grid-cols-3">
          <div>
            <label for="resolution-guard-percent" class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.settings.imageResolutionGuard.minAreaPercent') }}</label>
            <input id="resolution-guard-percent" v-model.number="form.min_area_percent" type="number" min="1" max="100" step="1" class="input w-full" data-testid="guard-percent" />
            <p class="mt-1.5 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.settings.imageResolutionGuard.minAreaHint') }}</p>
          </div>
          <div>
            <label for="resolution-guard-threshold" class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.settings.imageResolutionGuard.failureThreshold') }}</label>
            <input id="resolution-guard-threshold" v-model.number="form.failure_threshold" type="number" min="1" max="100" step="1" class="input w-full" data-testid="guard-threshold" />
            <p class="mt-1.5 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.settings.imageResolutionGuard.thresholdHint') }}</p>
          </div>
          <div>
            <label for="resolution-guard-cooldown" class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.settings.imageResolutionGuard.cooldownSeconds') }}</label>
            <input id="resolution-guard-cooldown" v-model.number="form.cooldown_seconds" type="number" min="10" max="86400" step="1" class="input w-full" data-testid="guard-cooldown" />
            <p class="mt-1.5 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.settings.imageResolutionGuard.cooldownHint') }}</p>
          </div>
        </div>
        <div class="space-y-4 border-t border-gray-100 pt-5 dark:border-dark-700">
          <div class="flex items-start justify-between gap-4">
            <div>
              <label id="resolution-guard-email-label" class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.settings.imageResolutionGuard.emailEnabled') }}</label>
              <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.settings.imageResolutionGuard.emailHint') }}</p>
            </div>
            <Toggle v-model="form.email_enabled" :disabled="busy || !loaded" aria-labelledby="resolution-guard-email-label" data-testid="guard-email" />
          </div>
          <div>
            <label for="resolution-guard-recipients" class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.settings.imageResolutionGuard.recipients') }}</label>
            <textarea id="resolution-guard-recipients" v-model="recipientsText" rows="2" class="input w-full resize-y" :placeholder="t('admin.settings.imageResolutionGuard.recipientsPlaceholder')" data-testid="guard-recipients" />
            <p class="mt-1.5 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ t('admin.settings.imageResolutionGuard.recipientsHint') }}</p>
          </div>
          <div v-if="view" class="rounded-lg bg-gray-50 p-3 text-xs leading-5 dark:bg-dark-900/50" data-testid="guard-delivery-status">
            <p class="font-medium text-gray-700 dark:text-gray-300">{{ t('admin.settings.imageResolutionGuard.savedRecipients') }}</p>
            <p class="mt-1 break-all text-gray-600 dark:text-gray-400">{{ view.effective_recipients?.join(' · ') || t('admin.settings.imageResolutionGuard.noRecipients') }}</p>
            <p class="mt-1 text-gray-500 dark:text-gray-400">{{ t(`admin.settings.imageResolutionGuard.recipientSources.${view.recipient_source || 'none'}`) }}</p>
          </div>
          <div v-if="form.email_enabled && view && (!view.smtp_configured || !view.effective_recipients?.length || view.warnings?.length)" class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs leading-5 text-amber-800 dark:border-amber-800 dark:bg-amber-950/30 dark:text-amber-200" role="status" data-testid="guard-mail-warning">
            <p v-if="!view.smtp_configured">{{ t('admin.settings.imageResolutionGuard.smtpMissing') }}</p>
            <p v-if="!view.effective_recipients?.length">{{ t('admin.settings.imageResolutionGuard.recipientsMissing') }}</p>
            <p v-for="warning in extraWarnings" :key="warning">{{ warningLabel(warning) }}</p>
            <p>{{ t('admin.settings.imageResolutionGuard.cooldownStillWorks') }}</p>
          </div>
        </div>
      </fieldset>
      <div class="border-l-2 border-sky-400 bg-sky-50 px-4 py-3 text-xs leading-5 text-sky-900 dark:border-sky-500 dark:bg-sky-950/30 dark:text-sky-200">
        <p>{{ t('admin.settings.imageResolutionGuard.deliveryHint') }}</p>
        <p class="mt-1">{{ t('admin.settings.imageResolutionGuard.disableHint') }}</p>
      </div>
      <div class="flex flex-col gap-3 border-t border-gray-100 pt-4 sm:flex-row sm:justify-end dark:border-dark-700">
        <button v-if="!loaded" type="button" class="btn btn-secondary" :disabled="busy" data-testid="guard-reload" @click="load">{{ t('admin.settings.imageResolutionGuard.retry') }}</button>
        <button type="button" class="btn btn-primary" :disabled="busy || !loaded" data-testid="guard-save" @click="save">{{ t(saving ? 'common.saving' : 'common.save') }}</button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import { useAppStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'
import { defaultImageResolutionGuardSettings, getImageResolutionGuardSettings, updateImageResolutionGuardSettings, type ImageResolutionGuardSettingsView } from '@/api/admin/imageResolutionGuard'

const { t } = useI18n()
const appStore = useAppStore()
const form = reactive(defaultImageResolutionGuardSettings())
const view = ref<ImageResolutionGuardSettingsView | null>(null)
const recipientsText = ref('')
const loading = ref(true)
const loaded = ref(false)
const saving = ref(false)
const error = ref('')
const busy = computed(() => loading.value || saving.value)
const extraWarnings = computed(() => (view.value?.warnings ?? []).filter(value => !['smtp_not_configured', 'no_effective_recipients'].includes(value)))
const warningLabel = (value: string) => value === 'mail_delivery_failed' ? t('admin.settings.imageResolutionGuard.deliveryFailed') : value

function apply(value: ImageResolutionGuardSettingsView) {
  Object.assign(form, defaultImageResolutionGuardSettings(), value)
  recipientsText.value = value.recipients?.join('\n') || ''
  view.value = value
}

async function load() {
  if (saving.value) return
  loading.value = true
  error.value = ''
  try {
    apply(await getImageResolutionGuardSettings())
    loaded.value = true
  } catch (cause) {
    loaded.value = false
    error.value = extractApiErrorMessage(cause, t('admin.settings.imageResolutionGuard.loadFailed'))
  } finally {
    loading.value = false
  }
}

async function save() {
  if (busy.value || !loaded.value) return
  error.value = ''
  const ranges = [[form.min_area_percent, 1, 100], [form.failure_threshold, 1, 100], [form.cooldown_seconds, 10, 86400]]
  if (!ranges.every(([value, min, max]) => Number.isSafeInteger(value) && value >= min && value <= max)) {
    error.value = t('admin.settings.imageResolutionGuard.invalidNumbers')
    return
  }
  const recipients = [...new Set(recipientsText.value.split(/[\s,;，；]+/).map(value => value.trim()).filter(Boolean))]
  if (recipients.some(value => !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value))) {
    error.value = t('admin.settings.imageResolutionGuard.invalidRecipients')
    return
  }
  saving.value = true
  try {
    apply(await updateImageResolutionGuardSettings({ ...form, recipients }))
    appStore.showSuccess(t('admin.settings.imageResolutionGuard.saved'))
  } catch (cause) {
    error.value = extractApiErrorMessage(cause, t('admin.settings.imageResolutionGuard.saveFailed'))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
