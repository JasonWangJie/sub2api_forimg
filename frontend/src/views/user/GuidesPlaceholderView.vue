<template>
  <AppLayout>
    <div class="mx-auto max-w-3xl">
      <div class="card p-8 text-center">
        <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ title }}</h1>
        <p class="mt-3 text-sm text-gray-500 dark:text-dark-400">{{ t('guides.comingSoon') }}</p>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'

const route = useRoute()
const { t } = useI18n()

const title = computed(() => {
  const key = typeof route.meta.titleKey === 'string' ? route.meta.titleKey : ''
  return key ? t(key) : String(route.meta.title || '')
})
</script>
