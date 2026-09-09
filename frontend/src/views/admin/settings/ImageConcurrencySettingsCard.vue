<template>
  <section class="card" data-testid="image-concurrency-settings">
    <div
      class="flex flex-col gap-3 border-b border-gray-100 px-6 py-4 sm:flex-row sm:items-start sm:justify-between dark:border-dark-700"
    >
      <div>
        <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
          {{ t("admin.settings.imageConcurrency.title") }}
        </h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
          {{ t("admin.settings.imageConcurrency.description") }}
        </p>
      </div>
      <div class="flex shrink-0 items-center gap-2 text-xs">
        <span class="text-gray-500 dark:text-gray-400">
          {{ t("admin.settings.imageConcurrency.source") }}
        </span>
        <span
          class="inline-flex min-h-6 items-center rounded-md px-2 py-1 font-medium"
          :class="source === 'system_settings'
            ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300'
            : 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300'"
          data-testid="image-concurrency-source"
        >
          {{ sourceLabel }}
        </span>
      </div>
    </div>

    <div class="space-y-5 p-6" :class="loading ? 'pointer-events-none opacity-60' : ''">
      <div class="flex items-start justify-between gap-4">
        <div>
          <label class="text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t("admin.settings.imageConcurrency.enabled") }}
          </label>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t("admin.settings.imageConcurrency.enabledHint") }}
          </p>
        </div>
        <Toggle v-model="form.enabled" :disabled="isBusy" />
      </div>

      <div class="grid gap-4 md:grid-cols-2">
        <div>
          <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t("admin.settings.imageConcurrency.maxConcurrentRequests") }}
          </label>
          <input
            v-model.number="form.max_concurrent_requests"
            type="number"
            min="0"
            step="1"
            class="input w-full"
            :disabled="isBusy"
            data-testid="image-concurrency-max"
          />
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t("admin.settings.imageConcurrency.maxConcurrentRequestsHint") }}
          </p>
        </div>

        <div>
          <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t("admin.settings.imageConcurrency.overflowMode") }}
          </label>
          <select
            v-model="form.overflow_mode"
            class="input w-full"
            :disabled="isBusy"
            data-testid="image-concurrency-mode"
          >
            <option value="wait">{{ t("admin.settings.imageConcurrency.overflowWait") }}</option>
            <option value="reject">{{ t("admin.settings.imageConcurrency.overflowReject") }}</option>
          </select>
        </div>

        <div :class="form.overflow_mode === 'wait' ? '' : 'opacity-50'">
          <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t("admin.settings.imageConcurrency.waitTimeoutSeconds") }}
          </label>
          <input
            v-model.number="form.wait_timeout_seconds"
            type="number"
            min="0"
            step="1"
            class="input w-full"
            :disabled="isBusy || form.overflow_mode !== 'wait'"
          />
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t("admin.settings.imageConcurrency.waitTimeoutSecondsHint") }}
          </p>
        </div>

        <div :class="form.overflow_mode === 'wait' ? '' : 'opacity-50'">
          <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ t("admin.settings.imageConcurrency.maxWaitingRequests") }}
          </label>
          <input
            v-model.number="form.max_waiting_requests"
            type="number"
            min="0"
            step="1"
            class="input w-full"
            :disabled="isBusy || form.overflow_mode !== 'wait'"
          />
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t("admin.settings.imageConcurrency.maxWaitingRequestsHint") }}
          </p>
        </div>
      </div>

      <div class="border-l-2 border-sky-400 bg-sky-50 px-4 py-3 text-xs text-sky-900 dark:border-sky-500 dark:bg-sky-950/30 dark:text-sky-200">
        <div class="flex gap-2">
          <Icon name="infoCircle" size="sm" class="mt-0.5 shrink-0" />
          <div class="space-y-1">
            <p>{{ t("admin.settings.imageConcurrency.hotReloadHint") }}</p>
            <p>{{ t("admin.settings.imageConcurrency.multiInstanceHint") }}</p>
          </div>
        </div>
      </div>

      <div class="border-t border-gray-100 pt-4 dark:border-dark-700">
        <p class="text-xs font-medium text-gray-600 dark:text-gray-300">
          {{ t("admin.settings.imageConcurrency.fallbackTitle") }}
        </p>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400" data-testid="image-concurrency-fallback">
          {{ fallbackSummary }}
        </p>
      </div>

      <div class="flex flex-col-reverse gap-3 sm:flex-row sm:justify-end">
        <button
          type="button"
          class="btn btn-secondary inline-flex items-center justify-center gap-2"
          :disabled="isBusy || source !== 'system_settings'"
          @click="resetToYAML"
        >
          <Icon name="refresh" size="sm" :class="resetting ? 'animate-spin' : ''" />
          {{ t("admin.settings.imageConcurrency.reset") }}
        </button>
        <button
          type="button"
          class="btn btn-primary inline-flex items-center justify-center gap-2"
          :disabled="isBusy"
          data-testid="image-concurrency-save"
          @click="save"
        >
          <Icon name="check" size="sm" />
          {{ t("admin.settings.imageConcurrency.save") }}
        </button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { useI18n } from "vue-i18n";
import { adminAPI } from "@/api";
import type {
  ImageConcurrencySettings,
  ImageConcurrencySettingsView,
} from "@/api/admin/settings";
import Icon from "@/components/icons/Icon.vue";
import Toggle from "@/components/common/Toggle.vue";
import { useAppStore } from "@/stores";
import { extractApiErrorMessage } from "@/utils/apiError";

const { t } = useI18n();
const appStore = useAppStore();

const loading = ref(true);
const saving = ref(false);
const resetting = ref(false);
const source = ref<ImageConcurrencySettingsView["source"]>("config_yaml");
const form = reactive<ImageConcurrencySettings>({
  enabled: false,
  max_concurrent_requests: 0,
  overflow_mode: "reject",
  wait_timeout_seconds: 30,
  max_waiting_requests: 100,
});
const fallback = reactive<ImageConcurrencySettings>({ ...form });

const isBusy = computed(() => loading.value || saving.value || resetting.value);
const sourceLabel = computed(() => t(
  source.value === "system_settings"
    ? "admin.settings.imageConcurrency.sourceSystem"
    : "admin.settings.imageConcurrency.sourceYaml",
));
const fallbackSummary = computed(() => t("admin.settings.imageConcurrency.fallbackSummary", {
  enabled: t(fallback.enabled ? "admin.settings.imageConcurrency.yes" : "admin.settings.imageConcurrency.no"),
  concurrency: fallback.max_concurrent_requests,
  mode: t(fallback.overflow_mode === "wait"
    ? "admin.settings.imageConcurrency.overflowWait"
    : "admin.settings.imageConcurrency.overflowReject"),
  timeout: fallback.wait_timeout_seconds,
  waiting: fallback.max_waiting_requests,
}));

function applyView(view: ImageConcurrencySettingsView): void {
  source.value = view.source;
  Object.assign(form, view.effective);
  Object.assign(fallback, view.fallback);
}

function hasValidNumbers(): boolean {
  return [
    form.max_concurrent_requests,
    form.wait_timeout_seconds,
    form.max_waiting_requests,
  ].every((value) => Number.isSafeInteger(value) && value >= 0);
}

async function load(): Promise<void> {
  loading.value = true;
  try {
    applyView(await adminAPI.settings.getImageConcurrencySettings());
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t("admin.settings.imageConcurrency.loadFailed")));
  } finally {
    loading.value = false;
  }
}

async function save(): Promise<void> {
  if (!hasValidNumbers()) {
    appStore.showError(t("admin.settings.imageConcurrency.invalidNonNegativeInteger"));
    return;
  }
  saving.value = true;
  try {
    applyView(await adminAPI.settings.updateImageConcurrencySettings({ ...form }));
    appStore.showSuccess(t("admin.settings.imageConcurrency.saveSuccess"));
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t("admin.settings.imageConcurrency.saveFailed")));
  } finally {
    saving.value = false;
  }
}

async function resetToYAML(): Promise<void> {
  if (!window.confirm(t("admin.settings.imageConcurrency.resetConfirm"))) {
    return;
  }
  resetting.value = true;
  try {
    applyView(await adminAPI.settings.resetImageConcurrencySettings());
    appStore.showSuccess(t("admin.settings.imageConcurrency.resetSuccess"));
  } catch (error: unknown) {
    appStore.showError(extractApiErrorMessage(error, t("admin.settings.imageConcurrency.resetFailed")));
  } finally {
    resetting.value = false;
  }
}

onMounted(load);
</script>
