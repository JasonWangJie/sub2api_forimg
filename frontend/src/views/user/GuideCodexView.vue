<template>
  <AppLayout>
    <div class="mx-auto max-w-7xl space-y-6">
      <!-- Hero -->
      <section class="card p-6">
        <div class="grid gap-5 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center">
          <div>
            <h1 class="text-3xl font-bold text-gray-950 dark:text-white">
              {{ t('guides.codex.title') }}
            </h1>
            <p class="mt-2 text-sm text-gray-500 dark:text-dark-300">
              {{ t('guides.codex.description') }}
            </p>
            <div class="mt-6 flex flex-wrap gap-3">
              <router-link to="/keys" class="btn btn-primary">
                {{ t('guides.codex.viewKeys') }}
              </router-link>
              <router-link to="/usage" class="btn btn-secondary">
                {{ t('guides.codex.viewUsage') }}
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
              <p class="mt-2 text-lg font-bold text-gray-950 dark:text-white">{{ item.value }}</p>
            </div>
          </div>
        </div>
      </section>

      <!-- Download -->
      <section class="card p-6">
        <p class="text-sm text-gray-700 dark:text-dark-200">
          {{ t('guides.codex.downloadLabel') }}
          <a
            :href="t('guides.codex.downloadUrl')"
            target="_blank"
            rel="noopener noreferrer"
            class="text-primary-600 underline-offset-2 hover:underline dark:text-primary-400"
          >
            {{ t('guides.codex.downloadUrl') }}
          </a>
        </p>
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
              {{ t('guides.codex.section1Title') }}
            </h2>
            <p class="mt-1 text-sm text-gray-500 dark:text-dark-300">
              {{ t('guides.codex.section1Desc') }}
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

      <!-- Section 02 -->
      <section class="card p-6">
        <div class="mb-5 flex items-center gap-3">
          <span
            class="flex h-10 w-10 items-center justify-center rounded-lg border border-gray-200 text-sm font-bold text-gray-700 dark:border-dark-700 dark:text-dark-100"
          >
            02
          </span>
          <h2 class="text-xl font-semibold text-gray-950 dark:text-white">
            {{ t('guides.codex.section2Title') }}
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
  { label: t('guides.codex.metaProtocol'), value: t('guides.codex.metaProtocolValue') },
  { label: t('guides.codex.metaPath'), value: t('guides.codex.metaPathValue') },
  { label: t('guides.codex.metaAuth'), value: t('guides.codex.metaAuthValue') },
  { label: t('guides.codex.metaRecord'), value: t('guides.codex.metaRecordValue') },
])

const features = computed(() => [
  {
    title: t('guides.codex.feature1Title'),
    desc: t('guides.codex.feature1Desc'),
    icon: 'M5.25 14.25h13.5m-13.5 0a3 3 0 01-3-3m3 3a3 3 0 100 6h13.5a3 3 0 100-6m-16.5-3a3 3 0 013-3h13.5a3 3 0 013 3m-19.5 0a4.5 4.5 0 01.9-2.7L5.737 5.1a3.375 3.375 0 012.7-1.35h7.126c1.062 0 2.062.5 2.7 1.35l2.587 3.45a4.5 4.5 0 01.9 2.7m0 0a3 3 0 01-3 3m0 3h.008v.008h-.008v-.008zm0-6h.008v.008h-.008v-.008zm-3 6h.008v.008h-.008v-.008zm0-6h.008v.008h-.008v-.008z',
  },
  {
    title: t('guides.codex.feature2Title'),
    desc: t('guides.codex.feature2Desc'),
    icon: 'M16.023 9.348h4.992v-.001M2.985 19.644v-4.992m0 0h4.992m-4.993 0l3.181 3.183a8.25 8.25 0 0013.803-3.7M4.031 9.865a8.25 8.25 0 0113.803-3.7l3.181 3.182m0-4.991v4.99',
  },
  {
    title: t('guides.codex.feature3Title'),
    desc: t('guides.codex.feature3Desc'),
    icon: 'M3 13.125C3 12.504 3.504 12 4.125 12h2.25c.621 0 1.125.504 1.125 1.125v6.75C7.5 20.496 6.996 21 6.375 21h-2.25A1.125 1.125 0 013 19.875v-6.75zM9.75 8.625c0-.621.504-1.125 1.125-1.125h2.25c.621 0 1.125.504 1.125 1.125v11.25c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V8.625zM16.5 4.125c0-.621.504-1.125 1.125-1.125h2.25C20.496 3 21 3.504 21 4.125v15.75c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V4.125z',
  },
  {
    title: t('guides.codex.feature4Title'),
    desc: t('guides.codex.feature4Desc'),
    icon: 'M9 12.75L11.25 15 15 9.75m-3-7.036A11.959 11.959 0 013.598 6 11.99 11.99 0 003 9.749c0 5.592 3.824 10.29 9 11.623 5.176-1.332 9-6.03 9-11.622 0-1.31-.21-2.571-.598-3.751h-.152c-3.196 0-6.1-1.248-8.25-3.285z',
  },
])

const steps = computed(() => [
  { title: t('guides.codex.step1Title'), desc: t('guides.codex.step1Desc') },
  { title: t('guides.codex.step2Title'), desc: t('guides.codex.step2Desc') },
  { title: t('guides.codex.step3Title'), desc: t('guides.codex.step3Desc') },
])
</script>
