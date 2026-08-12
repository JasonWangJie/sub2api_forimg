<template>
  <AppLayout>
    <div class="docs-page-layout">
      <div class="card docs-page-shell relative flex-1 min-h-0 overflow-hidden">
        <div class="flex h-full min-h-0 overflow-hidden">
          <aside v-show="tocVisible" class="toc-sidebar">
            <div class="toc-header">
              <span class="toc-title">{{ t('customPage.tableOfContents') }}</span>
              <button type="button" class="toc-close-btn" :aria-label="t('nav.collapse')" @click="tocVisible = false">
                <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <path d="M15 18l-6-6 6-6" />
                </svg>
              </button>
            </div>
            <nav class="toc-nav">
              <a
                v-for="item in tocItems"
                :key="item.id"
                :href="'#' + item.id"
                class="toc-item"
                :class="[`toc-level-${item.level}`, { 'toc-active': activeHeadingId === item.id }]"
                @click.prevent="scrollToHeading(item.id)"
              >
                {{ item.text }}
              </a>
            </nav>
          </aside>

          <button
            v-show="!tocVisible && tocItems.length > 0"
            type="button"
            class="toc-toggle-btn"
            @click="tocVisible = true"
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M3 12h18M3 6h18M3 18h18" />
            </svg>
            <span class="ml-1 text-xs">{{ t('customPage.tableOfContents') }}</span>
          </button>

          <div
            ref="markdownContainer"
            class="markdown-page-content docs-page-content flex-1 h-full min-w-0 overflow-auto p-6 md:p-10"
            v-html="renderedHtml"
            @scroll="onContentScroll"
          ></div>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAppStore } from '@/stores'
import { buildGuideDocsMarkdown } from './guideDocsContent'

interface TocItem {
  id: string
  text: string
  level: number
}

const { t, locale } = useI18n()
const appStore = useAppStore()

const renderedHtml = ref('')
const markdownContainer = ref<HTMLElement | null>(null)
const tocItems = ref<TocItem[]>([])
const tocVisible = ref(typeof window !== 'undefined' ? window.innerWidth > 768 : true)
const activeHeadingId = ref('')
let scrollRafId = 0

const apiV1Base = computed(() => {
  const configured = appStore.apiBaseUrl.trim()
  const fallback = typeof window !== 'undefined' ? window.location.origin : ''
  return (configured || fallback).replace(/\/+$/, '')
})

function generateHeadingId(text: string, index: number): string {
  const base = text
    .toLowerCase()
    .replace(/[^\w一-鿿]+/g, '-')
    .replace(/^-+|-+$/g, '')
  return base ? `${base}-${index}` : `heading-${index}`
}

async function renderMarkdown() {
  const markdown = buildGuideDocsMarkdown({
    locale: String(locale.value),
    siteName: appStore.siteName || 'Sub2API',
    apiBase: apiV1Base.value,
  })

  const html = marked.parse(markdown) as string
  const sanitized = DOMPurify.sanitize(html)

  const toc: TocItem[] = []
  let headingIndex = 0
  renderedHtml.value = sanitized.replace(
    /<(h[1-4])[^>]*>(.*?)<\/h[1-4]>/gi,
    (_, tag: string, content: string) => {
      const level = Number.parseInt(tag[1], 10)
      const text = content.replace(/<[^>]+>/g, '').trim()
      const id = generateHeadingId(text, headingIndex++)
      toc.push({ id, text, level })
      return `<${tag} id="${id}">${content}</${tag}>`
    },
  )
  tocItems.value = toc
  activeHeadingId.value = ''

  await nextTick()
  await nextTick()
  injectCopyButtons()
}

function scrollToHeading(id: string) {
  const container = markdownContainer.value
  if (!container) return
  const el = container.querySelector(`#${CSS.escape(id)}`)
  if (!el) return
  el.scrollIntoView({ behavior: 'smooth', block: 'start' })
  activeHeadingId.value = id
  if (window.innerWidth <= 640) {
    tocVisible.value = false
  }
}

function onContentScroll() {
  if (scrollRafId) return
  scrollRafId = requestAnimationFrame(() => {
    scrollRafId = 0
    const container = markdownContainer.value
    if (!container || tocItems.value.length === 0) return

    const containerRect = container.getBoundingClientRect()
    let current = ''
    for (const item of tocItems.value) {
      const el = container.querySelector(`#${CSS.escape(item.id)}`) as HTMLElement | null
      if (!el) continue
      if (el.getBoundingClientRect().top - containerRect.top <= 100) {
        current = item.id
      }
    }
    activeHeadingId.value = current
  })
}

function injectCopyButtons() {
  const container = markdownContainer.value
  if (!container) return

  container.querySelectorAll('pre').forEach((pre) => {
    if (pre.querySelector('.copy-btn')) return
    const btn = document.createElement('button')
    btn.className = 'copy-btn'
    btn.type = 'button'
    btn.textContent = t('customPage.copyCode')
    btn.addEventListener('click', async () => {
      const code = pre.querySelector('code')?.textContent ?? pre.textContent ?? ''
      try {
        await navigator.clipboard.writeText(code)
        btn.textContent = t('customPage.copiedCode')
        setTimeout(() => {
          btn.textContent = t('customPage.copyCode')
        }, 2000)
      } catch {
        btn.textContent = t('customPage.copyCodeFailed')
        setTimeout(() => {
          btn.textContent = t('customPage.copyCode')
        }, 2000)
      }
    })
    ;(pre as HTMLElement).style.position = 'relative'
    pre.appendChild(btn)
  })
}

watch(
  [locale, () => appStore.siteName, apiV1Base],
  () => {
    void renderMarkdown()
  },
  { immediate: true },
)

onMounted(async () => {
  if (!appStore.publicSettingsLoaded) {
    await appStore.fetchPublicSettings()
  }
})

onUnmounted(() => {
  if (scrollRafId) cancelAnimationFrame(scrollRafId)
})
</script>

<style scoped>
.docs-page-layout {
  @apply flex flex-col;
  height: calc(100vh - 64px - 4rem);
}

.docs-page-shell {
  @apply bg-white dark:bg-dark-800/80;
}

.toc-sidebar {
  @apply flex flex-col h-full border-r border-gray-200 dark:border-dark-600 bg-gray-50 dark:bg-dark-900/70;
  width: min(248px, 32%);
  min-width: 168px;
  max-width: 280px;
  overflow: hidden;
}

@media (max-width: 640px) {
  .toc-sidebar {
    position: absolute;
    left: 0;
    top: 0;
    z-index: 20;
    width: 72%;
    max-width: 260px;
    height: 100%;
    box-shadow: 8px 0 24px rgba(15, 23, 42, 0.12);
    @apply bg-white dark:bg-dark-900;
  }
}

.toc-header {
  @apply flex items-center justify-between px-4 py-3.5 border-b border-gray-200 dark:border-dark-600;
}

.toc-title {
  @apply text-sm font-semibold tracking-wide text-gray-800 dark:text-gray-100;
}

.toc-close-btn {
  @apply p-1 rounded text-gray-400 hover:text-gray-600 dark:text-dark-400 dark:hover:text-gray-200 hover:bg-gray-200 dark:hover:bg-dark-700 transition-colors;
}

.toc-nav {
  @apply flex-1 overflow-y-auto py-3 px-2;
}

.toc-item {
  @apply block px-2.5 py-1.5 text-sm rounded-md transition-colors truncate;
  @apply text-gray-600 dark:text-gray-300 hover:text-gray-950 dark:hover:text-white hover:bg-gray-100 dark:hover:bg-dark-700;
}

.toc-item.toc-active {
  @apply text-primary-700 dark:text-primary-300 bg-primary-50 dark:bg-primary-900/30 font-medium;
  box-shadow: inset 2px 0 0 #14b8a6;
}

.toc-level-1 {
  padding-left: 10px;
  font-weight: 600;
}
.toc-level-2 {
  padding-left: 20px;
}
.toc-level-3 {
  padding-left: 32px;
  font-size: 0.8125rem;
}
.toc-level-4 {
  padding-left: 44px;
  font-size: 0.8125rem;
}

.toc-toggle-btn {
  @apply absolute left-3 top-3 z-10 flex items-center px-2.5 py-1.5 rounded-lg text-sm;
  @apply bg-white dark:bg-dark-800 border border-gray-200 dark:border-dark-600;
  @apply text-gray-600 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-dark-700;
  @apply shadow-sm transition-colors cursor-pointer;
}

.docs-page-content {
  scroll-behavior: smooth;
}
</style>
