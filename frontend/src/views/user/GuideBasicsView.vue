<template>
  <AppLayout>
    <div class="mx-auto max-w-7xl space-y-6">
      <section class="card p-6">
        <h1 class="text-3xl font-bold text-gray-950 dark:text-white">
          {{ t('guides.basics.title') }}
        </h1>
        <p class="mt-2 text-sm text-gray-500 dark:text-dark-300">
          {{ t('guides.basics.description') }}
        </p>
      </section>

      <section class="card p-6">
        <div class="space-y-8">
          <div v-for="step in steps" :key="step.textKey" class="space-y-3">
            <p class="text-sm font-medium text-gray-800 dark:text-dark-100">
              {{ t(step.textKey) }}
            </p>
            <div
              v-for="image in step.images"
              :key="image"
              class="tutorial-image overflow-hidden rounded-lg border border-gray-200 dark:border-dark-700"
            >
              <LazyImage
                :src="imageSrc(image)"
                :alt="t('guides.basics.imageAlt')"
                root-margin="200px 0px"
              >
                <template #error>
                  <div
                    class="flex min-h-[160px] items-center justify-center px-4 py-8 text-sm text-gray-400 dark:text-dark-400"
                  >
                    {{ t('guides.basics.imageMissing') }}
                  </div>
                </template>
              </LazyImage>
            </div>
          </div>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import LazyImage from '@/components/common/LazyImage.vue'

const { t } = useI18n()

/** 静态资源目录：frontend/public/guides/basics/ */
const IMAGE_BASE = '/guides/basics'

const steps = [
  { textKey: 'guides.basics.step1', images: ['01-open-api-keys.png'] },
  { textKey: 'guides.basics.step2', images: ['02-create-key.png'] },
  { textKey: 'guides.basics.step3', images: ['03-select-group.png'] },
  {
    textKey: 'guides.basics.step4',
    images: ['04-endpoint-and-key.png', '05-tool-config.png'],
  },
  {
    textKey: 'guides.basics.step5',
    images: ['06-ccswitch-config-1.png', '07-ccswitch-config-2.png', '07-ccswitch-config-3.png'],
  },
  { textKey: 'guides.basics.step6', images: ['08-save-and-use.png'] },
]

function imageSrc(filename: string) {
  return `${IMAGE_BASE}/${filename}`
}
</script>

<style scoped>
.tutorial-image {
  width: 100%;
  min-height: 160px;
}

.tutorial-image :deep(.lazy-image) {
  width: 100%;
  height: auto;
  min-height: 160px;
  background: transparent;
}

.tutorial-image :deep(.lazy-image img) {
  display: block;
  width: 100%;
  height: auto;
  object-fit: contain;
}
</style>
