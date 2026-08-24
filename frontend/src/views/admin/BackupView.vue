<template>
    <div class="space-y-6">
      <!-- S3 Storage Config -->
      <div class="card p-6">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('admin.backup.s3.title') }}
            </h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.backup.s3.descriptionPrefix') }}
              <button type="button" class="text-primary-600 underline hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300" @click="showR2Guide = true">Cloudflare R2</button>
              {{ t('admin.backup.s3.descriptionSuffix') }}
            </p>
          </div>
        </div>
        <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.s3.endpoint') }}</label>
            <input v-model="s3Form.endpoint" class="input w-full" placeholder="https://<account_id>.r2.cloudflarestorage.com" />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.s3.region') }}</label>
            <input v-model="s3Form.region" class="input w-full" placeholder="auto" />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.s3.bucket') }}</label>
            <input v-model="s3Form.bucket" class="input w-full" />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.s3.prefix') }}</label>
            <input v-model="s3Form.prefix" class="input w-full" placeholder="backups/" />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.s3.accessKeyId') }}</label>
            <input v-model="s3Form.access_key_id" class="input w-full" />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.s3.secretAccessKey') }}</label>
            <input v-model="s3Form.secret_access_key" type="password" class="input w-full" :placeholder="s3SecretConfigured ? t('admin.backup.s3.secretConfigured') : ''" />
          </div>
          <label class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300 md:col-span-2">
            <input v-model="s3Form.force_path_style" type="checkbox" />
            <span>{{ t('admin.backup.s3.forcePathStyle') }}</span>
          </label>
        </div>
        <div class="mt-4 flex flex-wrap gap-2">
          <button type="button" class="btn btn-secondary btn-sm" :disabled="testingS3" @click="testS3">
            {{ testingS3 ? t('common.loading') : t('admin.backup.s3.testConnection') }}
          </button>
          <button type="button" class="btn btn-primary btn-sm" :disabled="savingS3" @click="saveS3Config">
            {{ savingS3 ? t('common.loading') : t('common.save') }}
          </button>
        </div>
      </div>

      <!-- Async image object storage -->
      <div class="card p-6">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('admin.backup.imageStorage.title') }}
            </h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.backup.imageStorage.description') }}
            </p>
          </div>
          <label class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
            <input v-model="imageStorageForm.enabled" type="checkbox" />
            <span>{{ t('admin.backup.imageStorage.enabled') }}</span>
          </label>
        </div>

        <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.backend') }}</label>
            <select v-model="imageStorageForm.backend" class="input w-full">
              <option value="oss">{{ t('admin.backup.imageStorage.backends.oss') }}</option>
              <option value="superbed">{{ t('admin.backup.imageStorage.backends.superbed') }}</option>
              <option value="local">{{ t('admin.backup.imageStorage.backends.local') }}</option>
            </select>
          </div>
          <div v-if="imageStorageForm.backend === 'oss'">
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.provider') }}</label>
            <select v-model="imageStorageForm.provider" class="input w-full">
              <option value="qiniu">{{ t('admin.backup.imageStorage.providers.qiniu') }}</option>
              <option value="aliyun">{{ t('admin.backup.imageStorage.providers.aliyun') }}</option>
              <option value="tencent">{{ t('admin.backup.imageStorage.providers.tencent') }}</option>
              <option value="custom_s3">{{ t('admin.backup.imageStorage.providers.custom_s3') }}</option>
            </select>
          </div>
          <div v-if="imageStorageForm.backend === 'oss' && imageStorageForm.provider === 'custom_s3'" class="flex items-end pb-2">
            <label class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
              <input v-model="imageStorageForm.reuse_backup_s3" type="checkbox" />
              <span>{{ t('admin.backup.imageStorage.reuseBackupS3') }}</span>
            </label>
          </div>

          <template v-if="imageStorageForm.backend === 'oss'">
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.bucket') }}</label>
              <input v-model="imageStorageForm.bucket" class="input w-full" :placeholder="imageStorageForm.reuse_backup_s3 ? t('admin.backup.imageStorage.bucketInherited') : ''" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.prefix') }}</label>
              <input v-model="imageStorageForm.prefix" class="input w-full" placeholder="images/" />
            </div>

            <template v-if="!imageStorageForm.reuse_backup_s3">
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.s3.endpoint') }}</label>
                <input v-model="imageStorageForm.endpoint" class="input w-full" :placeholder="t('admin.backup.imageStorage.endpointPlaceholder')" />
                <p class="mt-1 text-xs text-gray-400">{{ t('admin.backup.imageStorage.endpointHint') }}</p>
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.s3.region') }}</label>
                <input v-model="imageStorageForm.region" class="input w-full" :placeholder="imageStorageForm.provider === 'custom_s3' ? 'auto' : t('admin.backup.imageStorage.regionPlaceholder')" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.s3.accessKeyId') }}</label>
                <input v-model="imageStorageForm.access_key_id" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.s3.secretAccessKey') }}</label>
                <input v-model="imageStorageForm.secret_access_key" type="password" class="input w-full" :placeholder="imageStorageSecretConfigured ? t('admin.backup.s3.secretConfigured') : ''" />
              </div>
              <label class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300 md:col-span-2">
                <input v-model="imageStorageForm.force_path_style" type="checkbox" />
                <span>{{ t('admin.backup.s3.forcePathStyle') }}</span>
              </label>
            </template>

            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.objectPublicBaseUrl') }}</label>
              <input v-model="imageStorageForm.public_base_url" class="input w-full" :placeholder="t('admin.backup.imageStorage.publicBaseUrlPlaceholder')" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.presignExpiryHours') }}</label>
              <input v-model.number="imageStorageForm.presign_expiry_hours" type="number" min="1" class="input w-full" />
            </div>
          </template>

          <template v-else-if="imageStorageForm.backend === 'superbed'">
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.superbed.token') }}</label>
              <input v-model="imageStorageForm.superbed.token" type="password" class="input w-full" :placeholder="imageStorageSuperbedTokenConfigured ? t('admin.backup.s3.secretConfigured') : ''" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.superbed.categories') }}</label>
              <input v-model="imageStorageForm.superbed.categories" class="input w-full" :placeholder="t('admin.backup.imageStorage.superbed.categoriesPlaceholder')" />
            </div>
            <div class="md:col-span-2">
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.superbed.uploadUrl') }}</label>
              <input v-model="imageStorageForm.superbed.upload_url" class="input w-full" placeholder="https://api.superbed.cn/upload" />
              <p class="mt-1 text-xs text-gray-400">{{ t('admin.backup.imageStorage.superbed.help') }}</p>
            </div>
            <div class="md:col-span-2">
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.localUrl') }}</label>
              <input v-model="imageStorageForm.superbed.local_url" class="input w-full" :placeholder="t('admin.backup.imageStorage.localUrlPlaceholder')" />
              <p class="mt-1 text-xs text-gray-400">{{ t('admin.backup.imageStorage.localUrlHint') }}</p>
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.prefix') }}</label>
              <input v-model="imageStorageForm.prefix" class="input w-full" placeholder="images/" />
            </div>
          </template>

          <template v-else-if="imageStorageForm.backend === 'local'">
            <div class="md:col-span-2">
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.local.dataDir') }}</label>
              <input v-model="imageStorageForm.local.data_dir" class="input w-full" :placeholder="t('admin.backup.imageStorage.local.dataDirPlaceholder')" />
              <p class="mt-1 text-xs text-gray-400">{{ t('admin.backup.imageStorage.local.dataDirHint') }}</p>
            </div>
            <div class="md:col-span-2">
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.local.sitePublicBaseUrl') }}</label>
              <input v-model="imageStorageForm.public_base_url" class="input w-full" :placeholder="t('admin.backup.imageStorage.local.publicBaseUrlPlaceholder')" />
              <p class="mt-1 text-xs text-gray-400">{{ t('admin.backup.imageStorage.local.sitePublicBaseUrlHint') }}</p>
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.prefix') }}</label>
              <input v-model="imageStorageForm.prefix" class="input w-full" placeholder="images/" />
            </div>
            <div class="md:col-span-2">
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.localUrl') }}</label>
              <input v-model="imageStorageForm.local.local_url" class="input w-full" :placeholder="t('admin.backup.imageStorage.localUrlPlaceholder')" />
              <p class="mt-1 text-xs text-gray-400">{{ t('admin.backup.imageStorage.localUrlHint') }}</p>
            </div>
          </template>
        </div>

        <div class="mt-6 border-t border-gray-200 pt-5 dark:border-dark-700">
          <div class="mb-4">
            <h4 class="text-sm font-semibold text-gray-800 dark:text-gray-200">{{ t('admin.backup.imageStorage.runtimeTitle') }}</h4>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.backup.imageStorage.runtimeDescription') }}</p>
          </div>
          <div class="grid grid-cols-1 gap-3 md:grid-cols-2 lg:grid-cols-3">
            <div class="md:col-span-2 lg:col-span-3">
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.asyncPublicBaseUrl') }}</label>
              <input v-model="imageStorageForm.async_image.public_base_url" type="url" class="input w-full" placeholder="https://api.example.com" />
              <p class="mt-1 text-xs text-gray-400">{{ t('admin.backup.imageStorage.asyncPublicBaseUrlHint') }}</p>
            </div>
            <div class="md:col-span-2 lg:col-span-3 flex items-start gap-2 rounded-md border border-gray-200 p-3 dark:border-dark-700">
              <input id="async-auto-archive-to-library" v-model="imageStorageForm.async_image.auto_archive_to_library" type="checkbox" class="mt-0.5" />
              <label for="async-auto-archive-to-library" class="text-xs text-gray-600 dark:text-gray-400">
                <span class="font-medium text-gray-700 dark:text-gray-200">{{ t('admin.backup.imageStorage.autoArchiveToLibrary') }}</span>
                <span class="mt-1 block">{{ t('admin.backup.imageStorage.autoArchiveToLibraryHint') }}</span>
              </label>
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.workerConcurrency') }}</label>
              <input v-model.number="imageStorageForm.async_image.worker_concurrency" type="number" min="1" max="64" class="input w-full" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.executionTimeoutSeconds') }}</label>
              <input v-model.number="imageStorageForm.async_image.execution_timeout_seconds" type="number" min="30" class="input w-full" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.signedUrlExpirySeconds') }}</label>
              <input v-model.number="imageStorageForm.async_image.signed_url_expiry_seconds" type="number" min="60" class="input w-full" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.inputRetentionHours') }}</label>
              <input v-model.number="imageStorageForm.async_image.input_retention_hours" type="number" min="1" max="720" class="input w-full" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.uploadPerMinute') }}</label>
              <input v-model.number="imageStorageForm.async_image.upload_per_minute" type="number" min="1" max="1000" class="input w-full" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.maxInputGiBPerKey') }}</label>
              <input v-model.number="asyncInputMaxGiB" type="number" min="0.1" max="100" step="0.1" class="input w-full" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.taskRetentionDays') }}</label>
              <input v-model.number="imageStorageForm.async_image.task_retention_days" type="number" min="1" class="input w-full" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.resultRetentionDays') }}</label>
              <input v-model.number="imageStorageForm.async_image.result_retention_days" type="number" min="1" class="input w-full" />
            </div>
          </div>

          <details class="mt-4 border-t border-dashed border-gray-200 pt-4 dark:border-dark-700">
            <summary class="cursor-pointer text-sm font-medium text-gray-700 marker:text-gray-400 dark:text-gray-300">
              {{ t('admin.backup.imageStorage.advancedRuntime') }}
            </summary>
            <div class="mt-4 grid grid-cols-1 gap-3 md:grid-cols-2 lg:grid-cols-3">
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.workerLeaseSeconds') }}</label>
                <input v-model.number="imageStorageForm.async_image.worker_lease_seconds" type="number" min="45" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.recoveryIntervalSeconds') }}</label>
                <input v-model.number="imageStorageForm.async_image.recovery_interval_seconds" type="number" min="5" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.retryBackoffSeconds') }}</label>
                <input v-model.number="imageStorageForm.async_image.retry_backoff_seconds" type="number" min="1" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.storageRetryAttempts') }}</label>
                <input v-model.number="imageStorageForm.async_image.storage_retry_attempts" type="number" min="1" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.billingRetryAttempts') }}</label>
                <input v-model.number="imageStorageForm.async_image.billing_retry_attempts" type="number" min="1" class="input w-full" />
              </div>
              <div class="md:col-span-2 lg:col-span-3">
                <h5 class="text-xs font-semibold text-gray-700 dark:text-gray-300">{{ t('admin.backup.imageStorage.referenceTransportTitle') }}</h5>
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.openaiReferenceTransportMode') }}</label>
                <select v-model="imageStorageForm.async_image.openai_reference_transport_mode" class="input w-full">
                  <option value="passthrough">{{ t('admin.backup.imageStorage.referenceTransportPassthrough') }}</option>
                  <option value="local">{{ t('admin.backup.imageStorage.referenceTransportLocalMultipart') }}</option>
                  <option value="passthrough_fallback_local">{{ t('admin.backup.imageStorage.referenceTransportFallbackMultipart') }}</option>
                </select>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.backup.imageStorage.openaiReferenceTransportHint') }}</p>
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.geminiReferenceTransportMode') }}</label>
                <select v-model="imageStorageForm.async_image.gemini_reference_transport_mode" class="input w-full">
                  <option value="passthrough">{{ t('admin.backup.imageStorage.referenceTransportPassthrough') }}</option>
                  <option value="local">{{ t('admin.backup.imageStorage.referenceTransportLocalInlineData') }}</option>
                  <option value="passthrough_fallback_local">{{ t('admin.backup.imageStorage.referenceTransportFallbackInlineData') }}</option>
                </select>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.backup.imageStorage.geminiReferenceTransportHint') }}</p>
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.geminiAsyncMaxAccountSwitches') }}</label>
                <input v-model.number="imageStorageForm.async_image.gemini_async_max_account_switches" type="number" min="0" max="16" class="input w-full" />
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.backup.imageStorage.geminiAsyncMaxAccountSwitchesHint') }}</p>
              </div>
              <div class="md:col-span-2 lg:col-span-3">
                <h5 class="text-xs font-semibold text-gray-700 dark:text-gray-300">{{ t('admin.backup.imageStorage.referenceRetryTitle') }}</h5>
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.retryMaxAttempts') }}</label>
                <input v-model.number="imageStorageForm.async_image.reference_fetch_max_retries" type="number" min="0" max="5" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.retryBaseSeconds') }}</label>
                <input v-model.number="imageStorageForm.async_image.reference_fetch_retry_base_seconds" type="number" min="1" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.retryMaxSeconds') }}</label>
                <input v-model.number="imageStorageForm.async_image.reference_fetch_retry_max_seconds" type="number" min="1" class="input w-full" />
              </div>
              <div class="md:col-span-2 lg:col-span-3">
                <h5 class="text-xs font-semibold text-gray-700 dark:text-gray-300">{{ t('admin.backup.imageStorage.upstreamRetryTitle') }}</h5>
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.retryMaxAttempts') }}</label>
                <input v-model.number="imageStorageForm.async_image.upstream_transient_max_retries" type="number" min="0" max="6" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.retryBaseSeconds') }}</label>
                <input v-model.number="imageStorageForm.async_image.upstream_transient_retry_base_seconds" type="number" min="1" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.retryMaxSeconds') }}</label>
                <input v-model.number="imageStorageForm.async_image.upstream_transient_retry_max_seconds" type="number" min="1" class="input w-full" />
              </div>
              <div class="md:col-span-2 lg:col-span-3">
                <h5 class="text-xs font-semibold text-gray-700 dark:text-gray-300">{{ t('admin.backup.imageStorage.capacityRetryTitle') }}</h5>
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.retryMaxAttempts') }}</label>
                <input v-model.number="imageStorageForm.async_image.capacity_max_retries" type="number" min="0" max="10" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.retryBaseSeconds') }}</label>
                <input v-model.number="imageStorageForm.async_image.capacity_retry_base_seconds" type="number" min="1" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.retryMaxSeconds') }}</label>
                <input v-model.number="imageStorageForm.async_image.capacity_retry_max_seconds" type="number" min="1" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.totalMaxRetries') }}</label>
                <input v-model.number="imageStorageForm.async_image.total_max_retries" type="number" min="0" max="32" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.retryJitterPercent') }}</label>
                <input v-model.number="imageStorageForm.async_image.retry_jitter_percent" type="number" min="0" max="50" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.retryAfterMaxSeconds') }}</label>
                <input v-model.number="imageStorageForm.async_image.retry_after_max_seconds" type="number" min="1" max="3600" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.downloadMaxBytes') }}</label>
                <input v-model.number="imageStorageForm.async_image.download_max_bytes" type="number" min="1048576" max="67108864" step="1048576" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.downloadMaxPixels') }}</label>
                <input v-model.number="imageStorageForm.async_image.download_max_pixels" type="number" :min="ASYNC_IMAGE_DOWNLOAD_PIXEL_LIMITS.min" :max="ASYNC_IMAGE_DOWNLOAD_PIXEL_LIMITS.max" step="1000000" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.maxReferenceImages') }}</label>
                <input v-model.number="imageStorageForm.async_image.max_reference_images" type="number" min="1" max="16" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.maxReferenceTotalBytes') }}</label>
                <input v-model.number="imageStorageForm.async_image.max_reference_total_bytes" type="number" min="1048576" max="268435456" step="1048576" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.maxReferenceTotalPixels') }}</label>
                <input v-model.number="imageStorageForm.async_image.max_reference_total_pixels" type="number" min="1000000" max="320000000" step="1000000" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.downloadTimeoutSeconds') }}</label>
                <input v-model.number="imageStorageForm.async_image.download_timeout_seconds" type="number" min="1" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.uploadTimeoutSeconds') }}</label>
                <input v-model.number="imageStorageForm.async_image.upload_timeout_seconds" type="number" min="30" max="600" class="input w-full" />
              </div>
              <div>
                <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.downloadMaxRedirects') }}</label>
                <input v-model.number="imageStorageForm.async_image.download_max_redirects" type="number" min="0" max="10" class="input w-full" />
              </div>
			  <div class="md:col-span-2">
				<label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.geminiHalfKModels') }}</label>
				<input v-model="geminiHalfKModelsText" class="input w-full" :placeholder="t('admin.backup.imageStorage.geminiHalfKModelsPlaceholder')" />
			  </div>
			  <label class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
				<input v-model="imageStorageForm.async_image.prompt_preview_enabled" type="checkbox" />
				<span>{{ t('admin.backup.imageStorage.promptPreviewEnabled') }}</span>
			  </label>
			  <div>
				<label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.promptPreviewMaxChars') }}</label>
				<input v-model.number="imageStorageForm.async_image.prompt_preview_max_chars" type="number" min="16" max="500" class="input w-full" />
			  </div>
            </div>
          </details>
        </div>

        <div class="mt-6 border-t border-gray-200 pt-5 dark:border-dark-700">
          <div class="mb-4">
            <h4 class="text-sm font-semibold text-gray-800 dark:text-gray-200">{{ t('admin.backup.imageStorage.libraryTitle') }}</h4>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.backup.imageStorage.libraryDescription') }}</p>
          </div>
          <div class="grid grid-cols-1 gap-3 md:grid-cols-2 lg:grid-cols-4">
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.libraryRetentionDays') }}</label>
              <input v-model.number="imageStorageForm.image_library.retention_days" type="number" min="1" class="input w-full" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.libraryMaxItems') }}</label>
              <input v-model.number="imageStorageForm.image_library.max_items_per_user" type="number" min="1" class="input w-full" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.libraryMaxGiB') }}</label>
              <input v-model.number="libraryMaxGiB" type="number" min="0.1" step="0.1" class="input w-full" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.libraryMaxImageMiB') }}</label>
              <input v-model.number="libraryMaxImageMiB" type="number" min="1" step="1" class="input w-full" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.libraryMaxImageMP') }}</label>
              <input v-model.number="libraryMaxImageMP" type="number" min="1" step="1" class="input w-full" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.librarySignedUrlExpiry') }}</label>
              <input v-model.number="imageStorageForm.image_library.signed_url_expiry_seconds" type="number" min="60" class="input w-full" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.libraryImportRate') }}</label>
              <input v-model.number="imageStorageForm.image_library.import_per_minute" type="number" min="1" class="input w-full" />
            </div>
            <div>
              <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.imageStorage.libraryPublishRate') }}</label>
              <input v-model.number="imageStorageForm.image_library.publish_per_minute" type="number" min="1" class="input w-full" />
            </div>
          </div>
        </div>

        <div class="mt-4 flex flex-wrap gap-2">
          <button type="button" class="btn btn-secondary btn-sm" :disabled="testingImageStorage" @click="testImageStorage">
            {{ testingImageStorage ? t('common.loading') : t('admin.backup.s3.testConnection') }}
          </button>
          <button type="button" class="btn btn-primary btn-sm" :disabled="savingImageStorage" @click="saveImageStorageConfig">
            {{ savingImageStorage ? t('common.loading') : t('common.save') }}
          </button>
        </div>
      </div>

      <!-- Schedule Config -->
      <div class="card p-6">
        <div class="mb-4">
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.backup.schedule.title') }}
          </h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.backup.schedule.description') }}
          </p>
        </div>
        <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <label class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300 md:col-span-2">
            <input v-model="scheduleForm.enabled" type="checkbox" />
            <span>{{ t('admin.backup.schedule.enabled') }}</span>
          </label>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.schedule.cronExpr') }}</label>
            <input v-model="scheduleForm.cron_expr" class="input w-full" placeholder="0 2 * * *" />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.backup.schedule.cronHint') }}</p>
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.schedule.retainDays') }}</label>
            <input v-model.number="scheduleForm.retain_days" type="number" min="0" class="input w-full" />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.backup.schedule.retainDaysHint') }}</p>
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.schedule.retainCount') }}</label>
            <input v-model.number="scheduleForm.retain_count" type="number" min="0" class="input w-full" />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.backup.schedule.retainCountHint') }}</p>
          </div>
        </div>
        <div class="mt-4">
          <button type="button" class="btn btn-primary btn-sm" :disabled="savingSchedule" @click="saveSchedule">
            {{ savingSchedule ? t('common.loading') : t('common.save') }}
          </button>
        </div>
      </div>

      <!-- Backup Operations -->
      <div class="card p-6">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('admin.backup.operations.title') }}
            </h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.backup.operations.description') }}
            </p>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <div class="flex items-center gap-1">
              <label class="text-xs text-gray-600 dark:text-gray-400">{{ t('admin.backup.operations.expireDays') }}</label>
              <input v-model.number="manualExpireDays" type="number" min="0" class="input w-20 text-xs" />
            </div>
            <button type="button" class="btn btn-primary btn-sm" :disabled="creatingBackup" @click="createBackup">
              {{ creatingBackup ? t('admin.backup.operations.backing') : t('admin.backup.operations.createBackup') }}
            </button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="loadingBackups" @click="loadBackups">
              {{ loadingBackups ? t('common.loading') : t('common.refresh') }}
            </button>
          </div>
        </div>

        <div class="overflow-x-auto">
          <table class="w-full min-w-[800px] text-sm">
            <thead>
              <tr class="border-b border-gray-200 text-left text-xs uppercase tracking-wide text-gray-500 dark:border-dark-700 dark:text-gray-400">
                <th class="py-2 pr-4">ID</th>
                <th class="py-2 pr-4">{{ t('admin.backup.columns.status') }}</th>
                <th class="py-2 pr-4">{{ t('admin.backup.columns.fileName') }}</th>
                <th class="py-2 pr-4">{{ t('admin.backup.columns.size') }}</th>
                <th class="py-2 pr-4">{{ t('admin.backup.columns.parts') }}</th>
                <th class="py-2 pr-4">{{ t('admin.backup.columns.expiresAt') }}</th>
                <th class="py-2 pr-4">{{ t('admin.backup.columns.triggeredBy') }}</th>
                <th class="py-2 pr-4">{{ t('admin.backup.columns.startedAt') }}</th>
                <th class="py-2">{{ t('admin.backup.columns.actions') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="record in backups" :key="record.id" class="border-b border-gray-100 align-top dark:border-dark-800">
                <td class="py-3 pr-4 font-mono text-xs">{{ record.id }}</td>
                <td class="py-3 pr-4">
                  <span
                    class="rounded px-2 py-0.5 text-xs"
                    :class="statusClass(record.status)"
                  >
                    {{ record.status === 'running' && record.progress
                      ? t(`admin.backup.progress.${record.progress}`)
                      : t(`admin.backup.status.${record.status}`) }}
                  </span>
                </td>
                <td class="py-3 pr-4 text-xs">{{ record.file_name }}</td>
                <td class="py-3 pr-4 text-xs">{{ formatSize(record.size_bytes) }}</td>
                <td class="py-3 pr-4 text-xs">{{ record.parts?.length || (record.status === 'running' ? '-' : 1) }}</td>
                <td class="py-3 pr-4 text-xs">
                  {{ record.expires_at ? formatDate(record.expires_at) : t('admin.backup.neverExpire') }}
                </td>
                <td class="py-3 pr-4 text-xs">
                  {{ record.triggered_by === 'scheduled' ? t('admin.backup.trigger.scheduled') : t('admin.backup.trigger.manual') }}
                </td>
                <td class="py-3 pr-4 text-xs">{{ formatDate(record.started_at) }}</td>
                <td class="py-3 text-xs">
                  <div class="flex flex-wrap gap-1">
                    <button
                      v-if="record.status === 'completed'"
                      type="button"
                      class="btn btn-secondary btn-xs"
                      @click="downloadBackup(record.id)"
                    >
                      {{ t('admin.backup.actions.download') }}
                    </button>
                    <button
                      v-if="record.status === 'completed'"
                      type="button"
                      class="btn btn-secondary btn-xs"
                      :disabled="restoringId === record.id"
                      @click="restoreBackup(record.id)"
                    >
                      {{ restoringId === record.id ? t('common.loading') : t('admin.backup.actions.restore') }}
                    </button>
                    <button
                      v-if="record.status !== 'running'"
                      type="button"
                      class="btn btn-danger btn-xs"
                      @click="removeBackup(record.id)"
                    >
                      {{ t('common.delete') }}
                    </button>
                  </div>
                </td>
              </tr>
              <tr v-if="backups.length === 0">
                <td colspan="9" class="py-6 text-center text-sm text-gray-500 dark:text-gray-400">
                  {{ t('admin.backup.empty') }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- Cloudflare R2 Setup Guide Modal -->
    <teleport to="body">
      <transition name="modal">
        <div v-if="showR2Guide" class="fixed inset-0 z-50 flex items-center justify-center p-4" @mousedown.self="showR2Guide = false">
          <div class="fixed inset-0 bg-black/50" @click="showR2Guide = false"></div>
          <div class="relative max-h-[85vh] w-full max-w-2xl overflow-y-auto rounded-xl bg-white p-6 shadow-2xl dark:bg-dark-800">
            <button type="button" class="absolute right-4 top-4 text-gray-400 hover:text-gray-600 dark:hover:text-gray-200" @click="showR2Guide = false">
              <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" /></svg>
            </button>

            <h2 class="mb-4 text-lg font-bold text-gray-900 dark:text-white">{{ t('admin.backup.r2Guide.title') }}</h2>
            <p class="mb-4 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.backup.r2Guide.intro') }}</p>

            <!-- Step 1 -->
            <div class="mb-5">
              <h3 class="mb-2 flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white">
                <span class="flex h-6 w-6 items-center justify-center rounded-full bg-primary-100 text-xs font-bold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300">1</span>
                {{ t('admin.backup.r2Guide.step1.title') }}
              </h3>
              <ol class="ml-8 list-decimal space-y-1 text-sm text-gray-600 dark:text-gray-300">
                <li>{{ t('admin.backup.r2Guide.step1.line1') }}</li>
                <li>{{ t('admin.backup.r2Guide.step1.line2') }}</li>
                <li>{{ t('admin.backup.r2Guide.step1.line3') }}</li>
              </ol>
            </div>

            <!-- Step 2 -->
            <div class="mb-5">
              <h3 class="mb-2 flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white">
                <span class="flex h-6 w-6 items-center justify-center rounded-full bg-primary-100 text-xs font-bold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300">2</span>
                {{ t('admin.backup.r2Guide.step2.title') }}
              </h3>
              <ol class="ml-8 list-decimal space-y-1 text-sm text-gray-600 dark:text-gray-300">
                <li>{{ t('admin.backup.r2Guide.step2.line1') }}</li>
                <li>{{ t('admin.backup.r2Guide.step2.line2') }}</li>
                <li>{{ t('admin.backup.r2Guide.step2.line3') }}</li>
                <li>{{ t('admin.backup.r2Guide.step2.line4') }}</li>
              </ol>
              <div class="mt-2 rounded-lg bg-amber-50 p-3 text-xs text-amber-700 dark:bg-amber-900/20 dark:text-amber-300">
                {{ t('admin.backup.r2Guide.step2.warning') }}
              </div>
            </div>

            <!-- Step 3 -->
            <div class="mb-5">
              <h3 class="mb-2 flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white">
                <span class="flex h-6 w-6 items-center justify-center rounded-full bg-primary-100 text-xs font-bold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300">3</span>
                {{ t('admin.backup.r2Guide.step3.title') }}
              </h3>
              <p class="ml-8 text-sm text-gray-600 dark:text-gray-300">{{ t('admin.backup.r2Guide.step3.desc') }}</p>
              <code class="ml-8 mt-1 block rounded bg-gray-100 px-3 py-2 text-xs text-gray-800 dark:bg-dark-700 dark:text-gray-200">https://&lt;{{ t('admin.backup.r2Guide.step3.accountId') }}&gt;.r2.cloudflarestorage.com</code>
            </div>

            <!-- Step 4: Fill form -->
            <div class="mb-5">
              <h3 class="mb-2 flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white">
                <span class="flex h-6 w-6 items-center justify-center rounded-full bg-primary-100 text-xs font-bold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300">4</span>
                {{ t('admin.backup.r2Guide.step4.title') }}
              </h3>
              <div class="ml-8 overflow-hidden rounded-lg border border-gray-200 dark:border-dark-600">
                <table class="w-full text-sm">
                  <tbody>
                    <tr v-for="(row, i) in r2ConfigRows" :key="i" class="border-b border-gray-100 dark:border-dark-700 last:border-0">
                      <td class="whitespace-nowrap bg-gray-50 px-3 py-2 font-medium text-gray-700 dark:bg-dark-700 dark:text-gray-300">{{ row.field }}</td>
                      <td class="px-3 py-2 text-gray-600 dark:text-gray-400"><code class="text-xs">{{ row.value }}</code></td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>

            <!-- Free tier note -->
            <div class="rounded-lg bg-green-50 p-3 text-xs text-green-700 dark:bg-green-900/20 dark:text-green-300">
              {{ t('admin.backup.r2Guide.freeTier') }}
            </div>

            <div class="mt-4 text-right">
              <button type="button" class="btn btn-primary btn-sm" @click="showR2Guide = false">{{ t('common.close') }}</button>
            </div>
          </div>
        </div>
      </transition>
    </teleport>
    <!-- 分卷下载链接 -->
    <teleport to="body">
      <transition name="modal">
        <div
          v-if="downloadPartsModalOpen"
          class="fixed inset-0 z-50 flex items-center justify-center p-4"
          @mousedown.self="closeDownloadParts"
        >
          <div class="fixed inset-0 bg-black/50" @click="closeDownloadParts"></div>
          <div class="relative max-h-[85vh] w-full max-w-lg overflow-y-auto rounded-xl bg-white p-6 shadow-2xl dark:bg-dark-800">
            <button
              type="button"
              class="absolute right-4 top-4 text-gray-400 hover:text-gray-600 dark:hover:text-gray-200"
              :aria-label="t('common.close')"
              @click="closeDownloadParts"
            >
              <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" /></svg>
            </button>
            <h2 class="mb-1 text-lg font-bold text-gray-900 dark:text-white">{{ t('admin.backup.actions.downloadParts') }}</h2>
            <p class="mb-4 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.backup.actions.downloadPartsHint') }}</p>
            <div class="space-y-2">
              <div
                v-for="part in downloadParts"
                :key="part.index"
                class="flex items-center justify-between gap-3 rounded-lg border border-gray-200 px-3 py-2 dark:border-dark-600"
              >
                <span class="text-sm text-gray-700 dark:text-gray-300">
                  {{ t('admin.backup.actions.partLabel', { index: part.index }) }}
                  <span class="ml-2 text-xs text-gray-500 dark:text-gray-400">{{ formatSize(part.size_bytes) }}</span>
                </span>
                <a :href="part.url" class="btn btn-secondary btn-xs" rel="noopener">
                  {{ t('admin.backup.actions.download') }}
                </a>
              </div>
            </div>
            <div class="mt-4 text-right">
              <button type="button" class="btn btn-primary btn-sm" @click="closeDownloadParts">{{ t('common.close') }}</button>
            </div>
          </div>
        </div>
      </transition>
    </teleport>
    <TotpStepUpDialog :controller="backupStepUp" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import { useAppStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'
import type {
  BackupS3Config,
  BackupScheduleConfig,
  BackupRecord,
  BackupDownloadPart,
  ImageStorageConfig,
} from '@/api/admin/backup'
import { useStepUp, isStepUpBlocked, isStepUpCancelled, stepUpBlockReason } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import {
  ASYNC_IMAGE_DOWNLOAD_PIXEL_LIMITS,
  ASYNC_IMAGE_REFERENCE_RETRY_DEFAULTS,
} from './asyncImageRuntimeConfig'

const { t } = useI18n()
const appStore = useAppStore()
const backupStepUp = useStepUp()

// 敏感操作被 2FA 门控拦截时的统一提示。
function reportStepUpBlocked(error: unknown): boolean {
  if (!isStepUpBlocked(error)) return false
  appStore.showError(
    stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'
      ? t('stepUp.adminApiKeyForbidden')
      : t('stepUp.notEnabled')
  )
  return true
}

// S3 config
const s3Form = ref<BackupS3Config>({
  endpoint: '',
  region: 'auto',
  bucket: '',
  access_key_id: '',
  secret_access_key: '',
  prefix: 'backups/',
  force_path_style: false,
})
const s3SecretConfigured = ref(false)
const savingS3 = ref(false)
const testingS3 = ref(false)

// Async image object storage. Shares the S3 client with backups, so the default is
// to reuse the credentials configured above and only differ by prefix.
const imageStorageForm = ref<ImageStorageConfig>({
  enabled: false,
  backend: 'oss',
  provider: 'custom_s3',
  reuse_backup_s3: true,
  bucket: '',
  prefix: 'images/',
  public_base_url: '',
  presign_expiry_hours: 24,
  max_download_bytes: 33554432,
  endpoint: '',
  region: 'auto',
  access_key_id: '',
  secret_access_key: '',
  force_path_style: false,
  superbed: {
    token: '',
    categories: '',
    upload_url: 'https://api.superbed.cn/upload',
    local_url: '',
  },
  local: {
    data_dir: '',
    local_url: '',
  },
  async_image: {
    public_base_url: '',
    worker_concurrency: 4,
    worker_lease_seconds: 120,
    recovery_interval_seconds: 30,
    execution_timeout_seconds: 1200,
    storage_retry_attempts: 5,
    billing_retry_attempts: 10,
    retry_backoff_seconds: 30,
    ...ASYNC_IMAGE_REFERENCE_RETRY_DEFAULTS,
    download_max_bytes: 33554432,
    max_reference_images: 8,
    max_reference_total_bytes: 67108864,
    max_reference_total_pixels: 80000000,
    download_timeout_seconds: 30,
    download_max_redirects: 3,
    signed_url_expiry_seconds: 3600,
    input_retention_hours: 24,
    upload_per_minute: 20,
    max_input_bytes_per_key: 1024 * 1024 * 1024,
    upload_timeout_seconds: 300,
    task_retention_days: 90,
    result_retention_days: 90,
	gemini_half_k_models: [],
	prompt_preview_enabled: true,
	prompt_preview_max_chars: 160,
  },
  image_library: {
    retention_days: 90,
    max_items_per_user: 1000,
    max_bytes_per_user: 5 * 1024 * 1024 * 1024,
    max_image_bytes: 20 * 1024 * 1024,
    max_image_pixels: 40 * 1000 * 1000,
    signed_url_expiry_seconds: 3600,
    import_per_minute: 20,
    publish_per_minute: 10,
  },
})

const geminiHalfKModelsText = computed({
	get: () => imageStorageForm.value.async_image.gemini_half_k_models.join(', '),
	set: (value: string) => {
		imageStorageForm.value.async_image.gemini_half_k_models = value
			.split(',')
			.map(model => model.trim())
			.filter(Boolean)
	},
})

const asyncInputMaxGiB = computed({
  get: () => Number((imageStorageForm.value.async_image.max_input_bytes_per_key / (1024 ** 3)).toFixed(2)),
  set: (value: number) => {
    imageStorageForm.value.async_image.max_input_bytes_per_key = Math.max(1, Math.round(Number(value || 0) * (1024 ** 3)))
  },
})

const libraryMaxGiB = computed({
  get: () => Number((imageStorageForm.value.image_library.max_bytes_per_user / (1024 ** 3)).toFixed(2)),
  set: (value: number) => {
    imageStorageForm.value.image_library.max_bytes_per_user = Math.max(1, Math.round(Number(value || 0) * (1024 ** 3)))
  },
})

const libraryMaxImageMiB = computed({
  get: () => Number((imageStorageForm.value.image_library.max_image_bytes / (1024 ** 2)).toFixed(2)),
  set: (value: number) => {
    imageStorageForm.value.image_library.max_image_bytes = Math.max(1, Math.round(Number(value || 0) * (1024 ** 2)))
  },
})

const libraryMaxImageMP = computed({
  get: () => Number((imageStorageForm.value.image_library.max_image_pixels / 1_000_000).toFixed(2)),
  set: (value: number) => {
    imageStorageForm.value.image_library.max_image_pixels = Math.max(1, Math.round(Number(value || 0) * 1_000_000))
  },
})
const imageStorageSecretConfigured = ref(false)
const imageStorageSuperbedTokenConfigured = ref(false)
const savingImageStorage = ref(false)
const testingImageStorage = ref(false)

// Schedule config
const scheduleForm = ref<BackupScheduleConfig>({
  enabled: false,
  cron_expr: '0 2 * * *',
  retain_days: 14,
  retain_count: 10,
})
const savingSchedule = ref(false)

// Backups
const backups = ref<BackupRecord[]>([])
const loadingBackups = ref(false)
const creatingBackup = ref(false)
const restoringId = ref('')
const manualExpireDays = ref(14)
const downloadParts = ref<BackupDownloadPart[]>([])
const downloadPartsModalOpen = ref(false)

// Polling
const pollingTimer = ref<ReturnType<typeof setInterval> | null>(null)
const restoringPollingTimer = ref<ReturnType<typeof setInterval> | null>(null)
const MAX_POLL_COUNT = 900

function updateRecordInList(updated: BackupRecord) {
  const idx = backups.value.findIndex(r => r.id === updated.id)
  if (idx >= 0) {
    backups.value[idx] = updated
  }
}

function startPolling(backupId: string) {
  stopPolling()
  let count = 0
  pollingTimer.value = setInterval(async () => {
    if (count++ >= MAX_POLL_COUNT) {
      stopPolling()
      creatingBackup.value = false
      appStore.showWarning(t('admin.backup.operations.backupRunning'))
      return
    }
    try {
      const record = await adminAPI.backup.getBackup(backupId)
      updateRecordInList(record)
      if (record.status === 'completed' || record.status === 'failed') {
        stopPolling()
        creatingBackup.value = false
        if (record.status === 'completed') {
          appStore.showSuccess(t('admin.backup.operations.backupCreated'))
        } else {
          appStore.showError(record.error_message || t('admin.backup.operations.backupFailed'))
        }
        await loadBackups()
      }
    } catch {
      // 轮询失败时不中断
    }
  }, 2000)
}

function stopPolling() {
  if (pollingTimer.value) {
    clearInterval(pollingTimer.value)
    pollingTimer.value = null
  }
}

function startRestorePolling(backupId: string) {
  stopRestorePolling()
  let count = 0
  restoringPollingTimer.value = setInterval(async () => {
    if (count++ >= MAX_POLL_COUNT) {
      stopRestorePolling()
      restoringId.value = ''
      appStore.showWarning(t('admin.backup.operations.restoreRunning'))
      return
    }
    try {
      const record = await adminAPI.backup.getBackup(backupId)
      updateRecordInList(record)
      if (record.restore_status === 'completed' || record.restore_status === 'failed') {
        stopRestorePolling()
        restoringId.value = ''
        if (record.restore_status === 'completed') {
          appStore.showSuccess(t('admin.backup.actions.restoreSuccess'))
        } else {
          appStore.showError(record.restore_error || t('admin.backup.operations.restoreFailed'))
        }
        await loadBackups()
      }
    } catch {
      // 轮询失败时不中断
    }
  }, 2000)
}

function stopRestorePolling() {
  if (restoringPollingTimer.value) {
    clearInterval(restoringPollingTimer.value)
    restoringPollingTimer.value = null
  }
}

function handleVisibilityChange() {
  if (document.hidden) {
    stopPolling()
    stopRestorePolling()
  } else {
    // 标签页恢复时刷新列表，检查是否仍有活跃操作
    loadBackups().then(() => {
      const running = backups.value.find(r => r.status === 'running')
      if (running) {
        creatingBackup.value = true
        startPolling(running.id)
      }
      const restoring = backups.value.find(r => r.restore_status === 'running')
      if (restoring) {
        restoringId.value = restoring.id
        startRestorePolling(restoring.id)
      }
    })
  }
}

// R2 guide
const showR2Guide = ref(false)
const r2ConfigRows = computed(() => [
  { field: t('admin.backup.s3.endpoint'), value: 'https://<account_id>.r2.cloudflarestorage.com' },
  { field: t('admin.backup.s3.region'), value: 'auto' },
  { field: t('admin.backup.s3.bucket'), value: t('admin.backup.r2Guide.step4.bucketValue') },
  { field: t('admin.backup.s3.prefix'), value: 'backups/' },
  { field: 'Access Key ID', value: t('admin.backup.r2Guide.step4.fromStep2') },
  { field: 'Secret Access Key', value: t('admin.backup.r2Guide.step4.fromStep2') },
  { field: t('admin.backup.s3.forcePathStyle'), value: t('admin.backup.r2Guide.step4.unchecked') },
])

async function loadS3Config() {
  try {
    const cfg = await adminAPI.backup.getS3Config()
    s3Form.value = {
      endpoint: cfg.endpoint || '',
      region: cfg.region || 'auto',
      bucket: cfg.bucket || '',
      access_key_id: cfg.access_key_id || '',
      secret_access_key: '',
      prefix: cfg.prefix || 'backups/',
      force_path_style: cfg.force_path_style,
    }
    s3SecretConfigured.value = Boolean(cfg.access_key_id)
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  }
}

async function saveS3Config() {
  savingS3.value = true
  try {
    await backupStepUp.run(() => adminAPI.backup.updateS3Config(s3Form.value))
    appStore.showSuccess(t('admin.backup.s3.saved'))
    await loadS3Config()
  } catch (error) {
    if (isStepUpCancelled(error)) {
      savingS3.value = false
      return
    }
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  } finally {
    savingS3.value = false
  }
}

async function loadImageStorageConfig() {
  try {
    const { config, secret_configured, superbed_token_configured } = await adminAPI.backup.getImageStorageConfig()
    imageStorageForm.value = {
      ...imageStorageForm.value,
      ...config,
      backend: config.backend || 'oss',
      provider: config.provider || 'custom_s3',
      prefix: config.prefix || 'images/',
      region: config.region || ((config.provider || 'custom_s3') === 'custom_s3' ? 'auto' : ''),
      secret_access_key: '',
      superbed: {
        ...imageStorageForm.value.superbed,
        ...(config.superbed || {}),
        token: '',
        upload_url: config.superbed?.upload_url || 'https://api.superbed.cn/upload',
      },
      local: {
        ...imageStorageForm.value.local,
        ...(config.local || {}),
      },
      async_image: {
        ...imageStorageForm.value.async_image,
        ...(config.async_image || {}),
      },
      image_library: {
        ...imageStorageForm.value.image_library,
        ...(config.image_library || {}),
      },
    }
    imageStorageSecretConfigured.value = secret_configured
    imageStorageSuperbedTokenConfigured.value = Boolean(superbed_token_configured)
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  }
}

watch(
  () => imageStorageForm.value.backend,
  (backend) => {
    if (backend !== 'oss') {
      imageStorageForm.value.reuse_backup_s3 = false
    }
    if (backend === 'local') {
      imageStorageForm.value.provider = 'local'
    } else if (backend === 'superbed') {
      imageStorageForm.value.provider = 'superbed'
    } else if (imageStorageForm.value.provider === 'local' || imageStorageForm.value.provider === 'superbed') {
      imageStorageForm.value.provider = 'custom_s3'
      if (!imageStorageForm.value.region) imageStorageForm.value.region = 'auto'
    }
  },
)

watch(
  () => imageStorageForm.value.provider,
  (provider) => {
    // region/auto only applies to OSS providers; ignore when backend is local/superbed
    // so reloading a saved local config does not mutate leftover region fields.
    if (imageStorageForm.value.backend !== 'oss') return
    if (provider !== 'custom_s3') {
      imageStorageForm.value.reuse_backup_s3 = false
      if (imageStorageForm.value.region === 'auto') imageStorageForm.value.region = ''
      return
    }
    if (!imageStorageForm.value.region) imageStorageForm.value.region = 'auto'
  },
)

function payloadForImageStorageSave(): ImageStorageConfig {
  const payload: ImageStorageConfig = {
    ...imageStorageForm.value,
    superbed: { ...imageStorageForm.value.superbed },
    local: { ...imageStorageForm.value.local },
    async_image: { ...imageStorageForm.value.async_image },
    image_library: { ...imageStorageForm.value.image_library },
  }
  if (payload.backend === 'local') {
    payload.provider = 'local'
    payload.reuse_backup_s3 = false
    payload.bucket = ''
    payload.endpoint = ''
    payload.region = ''
    payload.access_key_id = ''
    payload.secret_access_key = ''
    payload.force_path_style = false
    payload.superbed = { token: '', categories: '', upload_url: '', local_url: '' }
  } else if (payload.backend === 'superbed') {
    payload.provider = 'superbed'
    payload.reuse_backup_s3 = false
    payload.bucket = ''
    payload.endpoint = ''
    payload.region = ''
    payload.access_key_id = ''
    payload.secret_access_key = ''
    payload.force_path_style = false
    payload.local = { data_dir: '', local_url: '' }
  }
  return payload
}

async function saveImageStorageConfig() {
  if (!validateImageStorageConfig()) return
  savingImageStorage.value = true
  try {
    await backupStepUp.run(() => adminAPI.backup.updateImageStorageConfig(payloadForImageStorageSave()))
    appStore.showSuccess(t('admin.backup.imageStorage.saved'))
    await loadImageStorageConfig()
  } catch (error) {
    if (isStepUpCancelled(error)) {
      savingImageStorage.value = false
      return
    }
    appStore.showError(extractApiErrorMessage(error, t('errors.networkError')))
  } finally {
    savingImageStorage.value = false
  }
}

async function testImageStorage() {
  if (!validateImageStorageConfig()) return
  testingImageStorage.value = true
  try {
    const result = await adminAPI.backup.testImageStorageConnection(payloadForImageStorageSave())
    if (result.ok) {
      appStore.showSuccess(result.message || t('admin.backup.s3.testSuccess'))
    } else {
      appStore.showError(result.message || t('admin.backup.s3.testFailed'))
    }
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('errors.networkError')))
  } finally {
    testingImageStorage.value = false
  }
}

function validateImageStorageConfig(): boolean {
  if (imageStorageForm.value.backend === 'oss') {
    if (
      imageStorageForm.value.provider !== 'custom_s3' &&
      !imageStorageForm.value.region.trim()
    ) {
      appStore.showError(t('admin.backup.imageStorage.regionRequired'))
      return false
    }
  }
  if (imageStorageForm.value.backend === 'superbed') {
    if (!imageStorageForm.value.superbed.token?.trim() && !imageStorageSuperbedTokenConfigured.value) {
      appStore.showError(t('admin.backup.imageStorage.superbed.tokenRequired'))
      return false
    }
    if (!imageStorageForm.value.superbed.local_url.trim()) {
      appStore.showError(t('admin.backup.imageStorage.localUrlRequired'))
      return false
    }
  }
  if (imageStorageForm.value.backend === 'local') {
    if (
      !imageStorageForm.value.local.local_url.trim() &&
      !imageStorageForm.value.public_base_url.trim() &&
      !imageStorageForm.value.async_image.public_base_url.trim()
    ) {
      appStore.showError(t('admin.backup.imageStorage.local.serveUrlRequired'))
      return false
    }
  }
  return true
}

async function testS3() {
  testingS3.value = true
  try {
    const result = await adminAPI.backup.testS3Connection(s3Form.value)
    if (result.ok) {
      appStore.showSuccess(result.message || t('admin.backup.s3.testSuccess'))
    } else {
      appStore.showError(result.message || t('admin.backup.s3.testFailed'))
    }
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  } finally {
    testingS3.value = false
  }
}

async function loadSchedule() {
  try {
    const cfg = await adminAPI.backup.getSchedule()
    scheduleForm.value = {
      enabled: cfg.enabled,
      cron_expr: cfg.cron_expr || '0 2 * * *',
      retain_days: cfg.retain_days || 14,
      retain_count: cfg.retain_count || 10,
    }
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  }
}

async function saveSchedule() {
  savingSchedule.value = true
  try {
    await adminAPI.backup.updateSchedule(scheduleForm.value)
    appStore.showSuccess(t('admin.backup.schedule.saved'))
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  } finally {
    savingSchedule.value = false
  }
}

async function loadBackups() {
  loadingBackups.value = true
  try {
    const result = await adminAPI.backup.listBackups()
    backups.value = result.items || []
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  } finally {
    loadingBackups.value = false
  }
}

async function createBackup() {
  creatingBackup.value = true
  try {
    const record = await backupStepUp.run(() => adminAPI.backup.createBackup({ expire_days: manualExpireDays.value }))
    // 插入到列表顶部
    backups.value.unshift(record)
    startPolling(record.id)
  } catch (error: any) {
    if (isStepUpCancelled(error)) {
      creatingBackup.value = false
      return
    }
    if (reportStepUpBlocked(error)) {
      creatingBackup.value = false
      return
    }
    if (error?.response?.status === 409) {
      appStore.showWarning(t('admin.backup.operations.alreadyInProgress'))
    } else {
      appStore.showError(error?.message || t('errors.networkError'))
    }
    creatingBackup.value = false
  }
}

async function downloadBackup(id: string) {
  try {
    const result = await backupStepUp.run(() => adminAPI.backup.getDownloadURL(id))
    if (result.parts && result.parts.length > 0) {
      downloadParts.value = result.parts
      downloadPartsModalOpen.value = true
      return
    }
    if (!result.url) {
      throw new Error(t('admin.backup.actions.downloadFailed'))
    }
    // 预签名 URL 带 attachment disposition，同页 anchor 导航直接触发下载；
    // 不用 window.open：step-up 弹窗 await 会耗尽瞬态用户激活，新标签页会被浏览器拦截。
    const link = document.createElement('a')
    link.href = result.url
    link.rel = 'noopener'
    link.click()
  } catch (error) {
    if (isStepUpCancelled(error)) return
    if (reportStepUpBlocked(error)) return
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  }
}

function closeDownloadParts() {
  downloadPartsModalOpen.value = false
  downloadParts.value = []
}

async function restoreBackup(id: string) {
  if (!window.confirm(t('admin.backup.actions.restoreConfirm'))) return
  const password = window.prompt(t('admin.backup.actions.restorePasswordPrompt'))
  if (!password) return
  restoringId.value = id
  try {
    const record = await backupStepUp.run(() => adminAPI.backup.restoreBackup(id, password))
    updateRecordInList(record)
    startRestorePolling(id)
  } catch (error: any) {
    restoringId.value = ''
    if (isStepUpCancelled(error)) return
    if (reportStepUpBlocked(error)) return
    // apiClient 拦截器把 HTTP 错误归一化为顶层 { status } 平面对象（无 response 字段）
    if (error?.status === 409 || error?.response?.status === 409) {
      appStore.showWarning(t('admin.backup.operations.restoreRunning'))
    } else {
      appStore.showError(error?.message || t('errors.networkError'))
    }
  }
}

async function removeBackup(id: string) {
  if (!window.confirm(t('admin.backup.actions.deleteConfirm'))) return
  try {
    await adminAPI.backup.deleteBackup(id)
    appStore.showSuccess(t('admin.backup.actions.deleted'))
    await loadBackups()
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  }
}

function statusClass(status: string): string {
  switch (status) {
    case 'completed':
      return 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300'
    case 'running':
      return 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300'
    case 'failed':
      return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
    default:
      return 'bg-gray-100 text-gray-700 dark:bg-dark-800 dark:text-gray-300'
  }
}

function formatSize(bytes: number): string {
  if (!bytes || bytes <= 0) return '-'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function formatDate(value?: string): string {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

onMounted(async () => {
  document.addEventListener('visibilitychange', handleVisibilityChange)
  await Promise.all([loadS3Config(), loadImageStorageConfig(), loadSchedule(), loadBackups()])

  // 如果有正在 running 的备份，恢复轮询
  const runningBackup = backups.value.find(r => r.status === 'running')
  if (runningBackup) {
    creatingBackup.value = true
    startPolling(runningBackup.id)
  }
  const restoringBackup = backups.value.find(r => r.restore_status === 'running')
  if (restoringBackup) {
    restoringId.value = restoringBackup.id
    startRestorePolling(restoringBackup.id)
  }
})

onBeforeUnmount(() => {
  stopPolling()
  stopRestorePolling()
  document.removeEventListener('visibilitychange', handleVisibilityChange)
})
</script>

<style scoped>
.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.2s ease;
}
.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}
</style>
