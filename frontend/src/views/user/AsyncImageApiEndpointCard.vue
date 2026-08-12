<template>
  <article :id="block.id" class="api-endpoint-card">
    <div class="api-endpoint-head">
      <span class="api-method" :class="block.method === 'GET' ? 'api-method-get' : 'api-method-post'">
        {{ block.method }}
      </span>
      <code class="api-path">{{ block.path }}</code>
    </div>
    <h3 class="api-endpoint-title">{{ block.title }}</h3>
    <p class="api-lead">{{ block.summary }}</p>
    <p v-if="block.contentType" class="api-meta">
      Content-Type: <code>{{ block.contentType }}</code>
    </p>

    <h4 class="api-h3">{{ labels.params }}</h4>
    <div class="api-table-wrap">
      <table class="api-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Type</th>
            <th>{{ labels.required }}</th>
            <th>{{ descHeader }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in block.params" :key="p.name">
            <td><code>{{ p.name }}</code></td>
            <td><code>{{ p.type }}</code></td>
            <td>
              <span :class="p.required ? 'api-badge-req' : 'api-badge-opt'">
                {{ p.required ? labels.required : labels.optional }}
              </span>
            </td>
            <td>{{ p.desc }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <h4 class="api-h3">{{ labels.requestBody }}</h4>
    <div class="api-code-block">
      <div class="api-code-toolbar">
        <span>JSON</span>
        <button type="button" class="api-copy" @click="$emit('copy', block.bodyExample)">
          {{ labels.copy }}
        </button>
      </div>
      <pre><code>{{ block.bodyExample }}</code></pre>
    </div>

    <h4 class="api-h3">{{ labels.acceptResponse }}</h4>
    <div class="api-code-block">
      <pre><code>{{ block.acceptExample }}</code></pre>
    </div>

    <div v-if="block.notes?.length" class="mt-4">
      <h4 class="api-h3">{{ labels.notes }}</h4>
      <ul class="api-bullet-list">
        <li v-for="(n, i) in block.notes" :key="i">{{ n }}</li>
      </ul>
    </div>
  </article>
</template>

<script setup lang="ts">
defineProps<{
  block: {
    id: string
    title: string
    method: 'POST' | 'GET'
    path: string
    summary: string
    contentType?: string
    params: Array<{ name: string; required: boolean; type: string; desc: string }>
    bodyExample: string
    acceptExample: string
    notes?: string[]
  }
  labels: {
    params: string
    required: string
    optional: string
    requestBody: string
    acceptResponse: string
    notes: string
    copy: string
  }
  descHeader: string
}>()

defineEmits<{ copy: [text: string] }>()
</script>

<style scoped>
.api-endpoint-card {
  @apply rounded-2xl border border-gray-200 dark:border-dark-600 bg-white dark:bg-dark-900 p-5 md:p-6;
}
.api-endpoint-head {
  @apply flex flex-wrap items-center gap-2;
}
.api-endpoint-title {
  @apply mt-3 text-lg font-semibold text-gray-950 dark:text-white;
}
.api-lead {
  @apply mt-1 text-sm text-gray-600 dark:text-gray-300 leading-relaxed;
}
.api-meta {
  @apply mt-2 text-xs text-gray-500 dark:text-gray-400;
}
.api-meta code {
  @apply text-primary-700 dark:text-primary-300;
}
.api-h3 {
  @apply mt-5 mb-2 text-sm font-semibold tracking-wide text-gray-800 dark:text-gray-100;
}
.api-method {
  @apply inline-flex items-center rounded-md px-2 py-0.5 text-xs font-bold tracking-wide text-white;
}
.api-method-post {
  background: #0d9488;
}
.api-method-get {
  background: #2563eb;
}
.api-path {
  @apply text-sm font-mono text-gray-800 dark:text-gray-100 break-all;
}
.api-table-wrap {
  @apply overflow-x-auto rounded-xl border border-gray-200 dark:border-dark-600 bg-white dark:bg-dark-900;
}
.api-table {
  @apply w-full text-sm text-left;
}
.api-table th {
  @apply px-3 py-2 bg-gray-50 dark:bg-dark-800 text-gray-600 dark:text-gray-300 font-semibold border-b border-gray-200 dark:border-dark-600 whitespace-nowrap;
}
.api-table td {
  @apply px-3 py-2.5 border-b border-gray-100 dark:border-dark-700 text-gray-700 dark:text-gray-300 align-top;
}
.api-table th:nth-child(1),
.api-table td:nth-child(1) {
  @apply whitespace-nowrap;
}
.api-table th:nth-child(2),
.api-table td:nth-child(2) {
  @apply whitespace-nowrap;
}
.api-table code {
  @apply text-xs font-mono text-primary-700 dark:text-primary-300;
}
.api-badge-req {
  @apply inline-flex items-center whitespace-nowrap rounded px-1.5 py-0.5 text-[11px] font-semibold bg-rose-50 text-rose-700 dark:bg-rose-900/40 dark:text-rose-300;
}
.api-badge-opt {
  @apply inline-flex items-center whitespace-nowrap rounded px-1.5 py-0.5 text-[11px] font-semibold bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300;
}
.api-table th:nth-child(3),
.api-table td:nth-child(3) {
  @apply whitespace-nowrap w-[4.5rem];
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
.api-bullet-list {
  @apply list-disc pl-5 space-y-1.5 text-sm text-gray-700 dark:text-gray-300;
}
</style>
