<template>
  <AppLayout>
    <div class="mx-auto max-w-7xl space-y-6">
      <!-- Hero -->
      <section class="card p-6">
        <div class="grid gap-5 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center">
          <div>
            <h1 class="text-3xl font-bold text-gray-950 dark:text-white">
              {{ t('guides.drawing.title') }}
            </h1>
            <p class="mt-2 text-sm text-gray-500 dark:text-dark-300">
              {{ t('guides.drawing.description') }}
            </p>
            <div class="mt-6 flex flex-wrap gap-3">
              <router-link to="/image-workbench" class="btn btn-primary">
                {{ t('guides.drawing.openWorkbench') }}
              </router-link>
              <router-link to="/keys" class="btn btn-secondary">
                {{ t('guides.drawing.viewKeys') }}
              </router-link>
              <router-link to="/async-image-tasks" class="btn btn-warning font-semibold">
                {{ t('guides.drawing.viewAsyncTasks') }}
              </router-link>
            </div>
          </div>
          <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <div
              v-for="item in metaItems"
              :key="item.label"
              class="rounded-lg border border-gray-200 p-4 dark:border-dark-700"
            >
              <p class="text-xs text-gray-500 dark:text-dark-300">{{ item.label }}</p>
              <p
                v-if="item.highlight"
                class="mt-2 text-lg font-bold text-gray-950 dark:text-white"
              >
                {{ item.prefix }}
                <span class="text-amber-600 dark:text-amber-400">{{ item.highlight }}</span>
              </p>
              <p v-else class="mt-2 text-lg font-bold text-gray-950 dark:text-white">
                {{ item.value }}
              </p>
            </div>
          </div>
        </div>
      </section>

      <!-- Section 01 -->
      <section class="card p-6">
        <div class="mb-5 flex items-center gap-3">
          <span
            class="flex h-10 w-10 items-center justify-center rounded-lg border border-gray-200 text-sm font-bold text-gray-700 dark:border-dark-700 dark:text-dark-100"
          >
            01
          </span>
          <div>
            <h2 class="text-xl font-semibold text-gray-950 dark:text-white">
              {{ t('guides.drawing.section1Title') }}
            </h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-300">
              {{ t('guides.drawing.section1Desc') }}
            </p>
          </div>
        </div>
        <div class="grid gap-4 lg:grid-cols-2">
          <div
            v-for="feature in features"
            :key="feature.title"
            class="rounded-lg border border-gray-200 p-5 dark:border-dark-700"
          >
            <div class="flex items-start gap-4">
              <div
                class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-gray-50 text-gray-700 dark:bg-dark-800 dark:text-dark-100"
              >
                <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
                  <path stroke-linecap="round" stroke-linejoin="round" :d="feature.icon" />
                </svg>
              </div>
              <div>
                <h3 class="font-semibold text-gray-950 dark:text-white">{{ feature.title }}</h3>
                <p class="mt-1 text-sm text-gray-500 dark:text-dark-300">{{ feature.desc }}</p>
              </div>
            </div>
          </div>
        </div>
      </section>

      <!-- Section 02: realtime vs async -->
      <section class="card p-6">
        <div class="mb-5 flex items-center gap-3">
          <span
            class="flex h-10 w-10 items-center justify-center rounded-lg border border-gray-200 text-sm font-bold text-gray-700 dark:border-dark-700 dark:text-dark-100"
          >
            02
          </span>
          <div>
            <h2 class="text-xl font-semibold text-gray-950 dark:text-white">
              {{ t('guides.drawing.section2TitlePrefix') }}
              <span class="text-amber-600 dark:text-amber-400">{{ t('guides.drawing.section2TitleAsync') }}</span>
            </h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-300">
              {{ t('guides.drawing.section2Desc') }}
            </p>
          </div>
        </div>

        <div class="grid gap-4 lg:grid-cols-2">
          <div class="rounded-lg border border-gray-200 p-5 dark:border-dark-700">
            <h3 class="font-semibold text-gray-950 dark:text-white">{{ t('guides.drawing.syncTitle') }}</h3>
            <p class="mt-2 text-sm text-gray-500 dark:text-dark-300">{{ t('guides.drawing.syncDesc') }}</p>
          </div>
          <div
            class="rounded-lg border-2 border-amber-400 bg-gradient-to-br from-amber-50 via-amber-50/80 to-orange-50/60 p-5 shadow-md shadow-amber-500/20 dark:border-amber-500 dark:from-amber-950/50 dark:via-amber-900/30 dark:to-orange-950/20 dark:shadow-amber-500/15"
          >
            <h3 class="font-semibold text-amber-700 dark:text-amber-300">
              {{ t('guides.drawing.asyncTitle') }}
            </h3>
            <p class="mt-2 text-sm text-gray-600 dark:text-dark-200">{{ t('guides.drawing.asyncDesc') }}</p>
            <p class="mt-3 text-sm font-medium text-amber-800 dark:text-amber-200">
              {{ t('guides.drawing.asyncNote') }}
            </p>
            <div class="mt-4 rounded-lg border border-amber-300/80 bg-white/80 p-4 dark:border-amber-700/60 dark:bg-dark-900/40">
              <h4 class="font-semibold text-gray-950 dark:text-white">{{ t('guides.drawing.asyncApiTitle') }}</h4>
              <p class="mt-2 text-sm text-gray-600 dark:text-dark-200">{{ t('guides.drawing.asyncApiDesc') }}</p>
              <router-link to="/guides/async-image-api" class="btn btn-warning mt-3 font-semibold">
                {{ t('guides.drawing.openAsyncApi') }}
              </router-link>
            </div>
          </div>
        </div>

        <div class="mt-4 rounded-lg border border-gray-200 p-5 dark:border-dark-700">
          <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <h3 class="font-semibold text-gray-950 dark:text-white">{{ t('guides.drawing.batchTitle') }}</h3>
              <p class="mt-1 text-sm text-gray-500 dark:text-dark-300">{{ t('guides.drawing.batchDesc') }}</p>
            </div>
            <router-link to="/batch-image" class="btn btn-secondary shrink-0">
              {{ t('guides.drawing.openBatch') }}
            </router-link>
          </div>
        </div>
      </section>

      <!-- Section 03 -->
      <section class="card p-6">
        <div class="mb-5 flex items-center gap-3">
          <span
            class="flex h-10 w-10 items-center justify-center rounded-lg border border-gray-200 text-sm font-bold text-gray-700 dark:border-dark-700 dark:text-dark-100"
          >
            03
          </span>
          <h2 class="text-xl font-semibold text-gray-950 dark:text-white">
            {{ t('guides.drawing.section3Title') }}
          </h2>
        </div>
        <div class="grid gap-4 md:grid-cols-3">
          <div
            v-for="(step, index) in steps"
            :key="step.title"
            class="rounded-lg border border-gray-200 p-5 dark:border-dark-700"
          >
            <span class="text-xs font-semibold text-primary-600 dark:text-primary-300">
              STEP {{ index + 1 }}
            </span>
            <h3 class="mt-2 font-semibold text-gray-950 dark:text-white">{{ step.title }}</h3>
            <p class="mt-2 text-sm text-gray-500 dark:text-dark-300">{{ step.desc }}</p>
          </div>
        </div>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'

const { t } = useI18n()

const metaItems = computed(() => [
  { label: t('guides.drawing.metaModel'), value: t('guides.drawing.metaModelValue') },
  { label: t('guides.drawing.metaRef'), value: t('guides.drawing.metaRefValue') },
  { label: t('guides.drawing.metaFormat'), value: t('guides.drawing.metaFormatValue') },
  {
    label: t('guides.drawing.metaMode'),
    value: t('guides.drawing.metaModeValue'),
    prefix: t('guides.drawing.metaModePrefix'),
    highlight: t('guides.drawing.metaModeAsync'),
  },
])

const features = computed(() => [
  {
    title: t('guides.drawing.feature1Title'),
    desc: t('guides.drawing.feature1Desc'),
    icon: 'M9.813 15.904L9 18.75l-.813-2.846a4.5 4.5 0 00-3.09-3.09L2.25 12l2.846-.813a4.5 4.5 0 003.09-3.09L9 5.25l.813 2.846a4.5 4.5 0 003.09 3.09L15.75 12l-2.846.813a4.5 4.5 0 00-3.09 3.09zM18.259 8.715L18 9.75l-.259-1.035a3.375 3.375 0 00-2.455-2.456L14.25 6l1.036-.259a3.375 3.375 0 002.455-2.456L18 2.25l.259 1.035a3.375 3.375 0 002.456 2.456L21.75 6l-1.035.259a3.375 3.375 0 00-2.456 2.456z',
  },
  {
    title: t('guides.drawing.feature2Title'),
    desc: t('guides.drawing.feature2Desc'),
    icon: 'M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5m-13.5-9L12 3m0 0l4.5 4.5M12 3v13.5',
  },
  {
    title: t('guides.drawing.feature3Title'),
    desc: t('guides.drawing.feature3Desc'),
    icon: 'M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.324.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 011.37.49l1.296 2.247a1.125 1.125 0 01-.26 1.431l-1.003.827c-.293.24-.438.613-.431.992a6.759 6.759 0 010 .255c-.007.378.138.75.43.99l1.005.828c.424.35.534.954.26 1.43l-1.298 2.247a1.125 1.125 0 01-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.57 6.57 0 01-.22.128c-.331.183-.581.495-.644.869l-.213 1.28c-.09.543-.56.941-1.11.941h-2.594c-.55 0-1.02-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 01-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 01-1.369-.49l-1.297-2.247a1.125 1.125 0 01.26-1.431l1.004-.827c.292-.24.437-.613.43-.992a6.932 6.932 0 010-.255c.007-.378-.138-.75-.43-.99l-1.004-.828a1.125 1.125 0 01-.26-1.43l1.297-2.247a1.125 1.125 0 011.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.087.22-.128.332-.183.582-.495.644-.869l.214-1.281zM15 12a3 3 0 11-6 0 3 3 0 016 0z',
  },
  {
    title: t('guides.drawing.feature4Title'),
    desc: t('guides.drawing.feature4Desc'),
    icon: 'M20 13V6a2 2 0 00-2-2H6a2 2 0 00-2 2v7m16 0v5a2 2 0 01-2 2H6a2 2 0 01-2-2v-5m16 0h-2.586a1 1 0 00-.707.293l-2.414 2.414a1 1 0 01-.707.293h-3.172a1 1 0 01-.707-.293l-2.414-2.414A1 1 0 006.586 13H4',
  },
])

const steps = computed(() => [
  { title: t('guides.drawing.step1Title'), desc: t('guides.drawing.step1Desc') },
  { title: t('guides.drawing.step2Title'), desc: t('guides.drawing.step2Desc') },
  { title: t('guides.drawing.step3Title'), desc: t('guides.drawing.step3Desc') },
])
</script>
