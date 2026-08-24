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
                v-for="item in doc.toc"
                :key="item.id"
                :href="'#' + item.id"
                class="toc-item"
                :class="[`toc-level-${item.level}`, { 'toc-active': activeHeadingId === item.id }]"
                @click.prevent="scrollToHeading(item.id)"
              >
                {{ item.label }}
              </a>
            </nav>
          </aside>

          <button v-show="!tocVisible" type="button" class="toc-toggle-btn" @click="tocVisible = true">
            <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M3 12h18M3 6h18M3 18h18" />
            </svg>
            <span class="ml-1 text-xs">{{ t('customPage.tableOfContents') }}</span>
          </button>

          <div ref="contentRef" class="api-doc-scroll flex-1 h-full min-w-0 overflow-auto" @scroll="onContentScroll">
            <div class="api-doc mx-auto max-w-5xl space-y-8 p-6 md:p-10">
              <header id="overview" class="api-hero">
                <p class="api-kicker">API</p>
                <h1 class="api-title">{{ doc.title }}</h1>
                <p class="api-subtitle">{{ doc.subtitle }}</p>
                <div class="mt-5 flex flex-wrap gap-2">
                  <span class="api-chip">task_id</span>
                  <span class="api-chip">{{ isEn ? 'Storage-configured URLs' : '按存储配置返回链接' }}</span>
                  <span class="api-chip">OpenAI / Gemini</span>
                </div>
                <ul class="api-bullet-list mt-6">
                  <li v-for="(b, i) in doc.overview.bullets" :key="i">{{ b }}</li>
                </ul>
              </header>

              <section id="auth" class="api-section">
                <h2 class="api-h2">{{ doc.toc[1].label }}</h2>
                <div class="api-code-block">
                  <div class="api-code-toolbar">
                    <span>{{ doc.labels.baseUrl }}</span>
                    <button type="button" class="api-copy" @click="copyText(doc.baseUrl)">{{ copyBtn }}</button>
                  </div>
                  <pre><code>{{ doc.baseUrl }}</code></pre>
                </div>
                <div class="api-code-block mt-3">
                  <pre><code>{{ doc.auth.header }}
{{ doc.auth.contentType }}</code></pre>
                </div>
                <p class="api-note mt-3">{{ doc.auth.idempotency }}</p>
              </section>

              <section id="openai" class="api-section">
                <h2 class="api-h2">{{ doc.toc[2].label }}</h2>
                <p class="api-lead">
                  {{
                    isEn
                      ? 'Use generations_oa for text-to-image and edits_oa for explicit image edits. generations_oa also promotes to edit mode when usable reference inputs are present. Accept: HTTP 202 + task_id.'
                      : '文生图使用 generations_oa，明确图生图使用 edits_oa；generations_oa 带有效参考图时也会自动进入编辑模式。受理成功：HTTP 202 + task_id。'
                  }}
                </p>
                <AsyncImageApiEndpointCard
                  class="mt-4"
                  :block="doc.openaiT2I"
                  :labels="doc.labels"
                  :desc-header="descHeader"
                  @copy="copyText"
                />
                <AsyncImageApiEndpointCard
                  class="mt-6"
                  :block="doc.openaiI2I"
                  :labels="doc.labels"
                  :desc-header="descHeader"
                  @copy="copyText"
                />
              </section>

              <section id="gemini-bb" class="api-section">
                <h2 class="api-h2">{{ isEn ? 'Gemini BB' : 'Gemini BB' }}</h2>
                <p class="api-lead">
                  {{
                    isEn
                      ? 'Chat Completions-compatible Gemini endpoint for text-to-image and image-to-image. Use image_url content parts for references; stream is disabled.'
                      : 'Chat Completions 兼容的 Gemini 入口，文生图和图生图都使用同一路径；参考图放入 image_url content part，异步接口不支持流式。'
                  }}
                </p>
                <AsyncImageApiEndpointCard
                  class="mt-4"
                  :block="doc.geminiBBT2I"
                  :labels="doc.labels"
                  :desc-header="descHeader"
                  @copy="copyText"
                />
                <AsyncImageApiEndpointCard
                  class="mt-6"
                  :block="doc.geminiBBI2I"
                  :labels="doc.labels"
                  :desc-header="descHeader"
                  @copy="copyText"
                />
              </section>

              <section id="gemini-sc" class="api-section">
                <h2 class="api-h2">{{ isEn ? 'Gemini SC' : 'Gemini SC' }}</h2>
                <p class="api-lead">
                  {{
                    isEn
                      ? 'JSON-oriented Gemini endpoint for text-to-image and image-to-image. Use image_urls for references and size for ratio or pixel-size aliases. Accept: HTTP 202.'
                      : 'JSON 风格的 Gemini 入口，文生图和图生图共用路径；图生图通过 image_urls 传参考图，size 支持比例或像素尺寸别名。受理成功：HTTP 202。'
                  }}
                </p>
                <AsyncImageApiEndpointCard
                  class="mt-4"
                  :block="doc.geminiT2I"
                  :labels="doc.labels"
                  :desc-header="descHeader"
                  @copy="copyText"
                />
                <AsyncImageApiEndpointCard
                  class="mt-6"
                  :block="doc.geminiI2I"
                  :labels="doc.labels"
                  :desc-header="descHeader"
                  @copy="copyText"
                />
                <AsyncImageApiEndpointCard
                  class="mt-6"
                  :block="doc.scUpload"
                  :labels="doc.labels"
                  :desc-header="descHeader"
                  @copy="copyText"
                />
              </section>

              <section :id="doc.query.id" class="api-section">
                <h2 class="api-h2">{{ doc.query.title }}</h2>
                <div class="space-y-2">
                  <div class="api-endpoint-head">
                    <span class="api-method api-method-get">{{ doc.query.method }}</span>
                    <code class="api-path">{{ doc.query.path }}</code>
                    <span class="api-path-tag">OpenAI / Gemini</span>
                  </div>
                </div>
                <p class="api-lead mt-3">{{ doc.query.summary }}</p>
                <p class="api-note mt-2">
                  {{ isEn ? 'Gemini SC compatibility alias: ' : 'Gemini SC 兼容查询别名：' }}<code>{{ doc.query.aliasPath }}</code>
                </p>

                <h3 class="api-h3">{{ doc.labels.statusTable }}</h3>
                <div class="api-table-wrap">
                  <table class="api-table">
                    <thead>
                      <tr>
                        <th>status</th>
                        <th>{{ descHeader }}</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="row in doc.query.statuses" :key="row.status">
                        <td><code>{{ row.status }}</code></td>
                        <td>{{ row.meaning }}</td>
                      </tr>
                    </tbody>
                  </table>
                </div>

                <h3 class="api-h3">{{ isEn ? 'Response examples' : '响应示例' }}</h3>
                <div class="grid gap-3 lg:grid-cols-3">
                  <div class="api-code-block">
                    <div class="api-code-toolbar"><span>queued</span></div>
                    <pre><code>{{ doc.query.queuedExample }}</code></pre>
                  </div>
                  <div class="api-code-block">
                    <div class="api-code-toolbar"><span>succeeded</span></div>
                    <pre><code>{{ doc.query.successExample }}</code></pre>
                  </div>
                  <div class="api-code-block">
                    <div class="api-code-toolbar"><span>failed</span></div>
                    <pre><code>{{ doc.query.failedExample }}</code></pre>
                  </div>
                </div>
              </section>

              <section id="limits" class="api-section">
                <h2 class="api-h2">{{ doc.limits.title }}</h2>
                <ul class="api-bullet-list">
                  <li v-for="(b, i) in doc.limits.bullets" :key="i">{{ b }}</li>
                </ul>
              </section>

              <section id="errors" class="api-section">
                <h2 class="api-h2">{{ doc.errors.title }}</h2>
                <div class="api-table-wrap">
                  <table class="api-table">
                    <thead>
                      <tr>
                        <th>HTTP</th>
                        <th>{{ descHeader }}</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="row in doc.errors.rows" :key="row.status">
                        <td><code>{{ row.status }}</code></td>
                        <td>{{ row.meaning }}</td>
                      </tr>
                    </tbody>
                  </table>
                </div>
              </section>

              <section id="oss" class="api-section api-oss">
                <h2 class="api-h2">{{ doc.oss.title }}</h2>
                <ul class="api-bullet-list">
                  <li v-for="(b, i) in doc.oss.bullets" :key="i">{{ b }}</li>
                </ul>
                <div class="mt-5 flex flex-wrap gap-3">
                  <router-link to="/guides/drawing" class="btn btn-secondary">
                    {{ isEn ? 'Drawing Guide' : '绘图教程' }}
                  </router-link>
                  <router-link to="/keys" class="btn btn-secondary">
                    {{ isEn ? 'API Keys' : '查看密钥' }}
                  </router-link>
                  <router-link to="/async-image-tasks" class="btn btn-primary">
                    {{ isEn ? 'Async Tasks' : '异步生图任务' }}
                  </router-link>
                </div>
              </section>
            </div>
          </div>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import { useAppStore } from '@/stores'
import AsyncImageApiEndpointCard from './AsyncImageApiEndpointCard.vue'
import { getAsyncImageApiDoc } from './guideAsyncImageApiContent'

const { t, locale } = useI18n()
const appStore = useAppStore()
const contentRef = ref<HTMLElement | null>(null)
const tocVisible = ref(typeof window !== 'undefined' ? window.innerWidth > 768 : true)
const activeHeadingId = ref('overview')
const copyBtn = ref('')
let scrollRafId = 0
let copyTimer = 0

const apiRoot = computed(() => {
  const configured = appStore.apiBaseUrl.trim()
  const fallback = typeof window !== 'undefined' ? window.location.origin : ''
  return (configured || fallback).replace(/\/+$/, '')
})
const isEn = computed(() => String(locale.value).toLowerCase().startsWith('en'))
const doc = computed(() => getAsyncImageApiDoc(String(locale.value), apiRoot.value))
const descHeader = computed(() => (isEn.value ? 'Description' : '说明'))

copyBtn.value = getAsyncImageApiDoc(String(locale.value), apiRoot.value).labels.copy

function scrollToHeading(id: string) {
  const container = contentRef.value
  if (!container) return
  const el = container.querySelector(`#${CSS.escape(id)}`)
  if (!el) return
  el.scrollIntoView({ behavior: 'smooth', block: 'start' })
  activeHeadingId.value = id
  if (window.innerWidth <= 640) tocVisible.value = false
}

function onContentScroll() {
  if (scrollRafId) return
  scrollRafId = requestAnimationFrame(() => {
    scrollRafId = 0
    const container = contentRef.value
    if (!container) return
    const ids = doc.value.toc.map((x) => x.id)
    const top = container.getBoundingClientRect().top
    let current = ids[0]
    for (const id of ids) {
      const el = container.querySelector(`#${CSS.escape(id)}`) as HTMLElement | null
      if (el && el.getBoundingClientRect().top - top <= 120) current = id
    }
    activeHeadingId.value = current
  })
}

async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    copyBtn.value = doc.value.labels.copied
    window.clearTimeout(copyTimer)
    copyTimer = window.setTimeout(() => {
      copyBtn.value = doc.value.labels.copy
    }, 1600)
  } catch {
    /* ignore */
  }
}
</script>

<style scoped>
.docs-page-layout {
  @apply flex flex-col;
  height: calc(100vh - 64px - 4rem);
}
.docs-page-shell {
  @apply bg-white dark:bg-dark-800;
}
.toc-sidebar {
  @apply flex flex-col h-full border-r border-gray-200 dark:border-dark-600 bg-gray-50 dark:bg-dark-900;
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
    @apply bg-white dark:bg-dark-900 shadow-lg;
  }
}
.toc-header {
  @apply flex items-center justify-between px-4 py-3.5 border-b border-gray-200 dark:border-dark-600;
}
.toc-title {
  @apply text-sm font-semibold text-gray-800 dark:text-gray-100;
}
.toc-close-btn {
  @apply p-1 rounded text-gray-400 hover:bg-gray-200 dark:hover:bg-dark-700;
}
.toc-nav {
  @apply flex-1 overflow-y-auto py-3 px-2;
}
.toc-item {
  @apply block px-2.5 py-1.5 text-sm rounded-md truncate text-gray-600 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-dark-700;
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
  padding-left: 22px;
  font-size: 0.8125rem;
}
.toc-toggle-btn {
  @apply absolute left-3 top-3 z-10 flex items-center px-2.5 py-1.5 rounded-lg text-sm;
  @apply bg-white dark:bg-dark-800 border border-gray-200 dark:border-dark-600 shadow-sm text-gray-600 dark:text-gray-300;
}

.api-doc-scroll {
  scroll-behavior: smooth;
  @apply bg-gray-50/80 dark:bg-dark-950/40;
}

.api-hero {
  @apply rounded-2xl border border-gray-200 dark:border-dark-600 p-6 md:p-8;
  @apply bg-white dark:bg-dark-900;
}
.api-kicker {
  @apply text-xs font-semibold tracking-[0.2em] uppercase text-primary-600 dark:text-primary-300;
}
.api-title {
  @apply mt-2 text-3xl font-bold text-gray-950 dark:text-white;
}
.api-subtitle {
  @apply mt-3 text-sm leading-relaxed text-gray-600 dark:text-gray-300;
}
.api-chip {
  @apply inline-flex items-center rounded-full border border-primary-200 dark:border-primary-800 px-2.5 py-1 text-xs font-medium text-primary-700 dark:text-primary-300 bg-primary-50 dark:bg-primary-900/40;
}
.api-section {
  @apply scroll-mt-6;
}
.api-h2 {
  @apply text-2xl font-bold text-gray-950 dark:text-white mb-3 pb-2 border-b border-gray-200 dark:border-dark-600;
}
.api-h3 {
  @apply mt-5 mb-2 text-sm font-semibold tracking-wide text-gray-800 dark:text-gray-100;
}
.api-lead {
  @apply text-sm text-gray-600 dark:text-gray-300 leading-relaxed;
}
.api-note {
  @apply text-sm text-gray-500 dark:text-gray-400;
}
.api-bullet-list {
  @apply list-disc pl-5 space-y-1.5 text-sm text-gray-700 dark:text-gray-300;
}
.api-endpoint-head {
  @apply flex flex-wrap items-center gap-2;
}
.api-method {
  @apply inline-flex items-center rounded-md px-2 py-0.5 text-xs font-bold tracking-wide text-white;
}
.api-method-get {
  background: #2563eb;
}
.api-path {
  @apply text-sm font-mono text-gray-800 dark:text-gray-100 break-all;
}
.api-path-tag {
  @apply inline-flex items-center rounded px-1.5 py-0.5 text-[11px] font-semibold text-gray-600 dark:text-gray-300 bg-gray-100 dark:bg-dark-700;
}
.api-table-wrap {
  @apply overflow-x-auto rounded-xl border border-gray-200 dark:border-dark-600 bg-white dark:bg-dark-900;
}
.api-table {
  @apply w-full text-sm text-left;
}
.api-table th {
  @apply px-3 py-2 bg-gray-50 dark:bg-dark-800 text-gray-600 dark:text-gray-300 font-semibold border-b border-gray-200 dark:border-dark-600;
}
.api-table td {
  @apply px-3 py-2.5 border-b border-gray-100 dark:border-dark-700 text-gray-700 dark:text-gray-300;
}
.api-table code {
  @apply text-xs font-mono text-primary-700 dark:text-primary-300;
}
.api-code-block {
  @apply rounded-xl overflow-hidden border border-gray-800 dark:border-dark-600 bg-gray-950 text-gray-100;
}
.api-code-toolbar {
  @apply flex items-center justify-between px-3 py-2 text-xs text-gray-400 border-b border-white/10;
}
.api-code-block pre {
  @apply m-0 p-4 overflow-x-auto text-xs leading-relaxed whitespace-pre-wrap text-gray-100;
}
.api-copy {
  @apply rounded px-2 py-0.5 text-xs text-gray-200 hover:bg-white/10 transition-colors;
}
.api-oss {
  @apply rounded-2xl border border-amber-200 dark:border-amber-800/60 bg-amber-50 dark:bg-amber-950/40 p-5 md:p-6;
}
</style>
