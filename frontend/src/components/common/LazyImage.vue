<template>
  <div ref="target" class="lazy-image" :class="{ 'is-loaded': Boolean(displaySrc) && !failed, 'is-failed': failed }">
    <img
      v-if="displaySrc && !failed"
      :src="displaySrc"
      :alt="alt"
      loading="lazy"
      decoding="async"
      @error="handleError"
      @load="emit('load')"
    />
    <slot v-else-if="failed" name="error">
      <span class="lazy-image__fallback" aria-hidden="true"></span>
    </slot>
    <slot v-else name="placeholder">
      <span class="lazy-image__placeholder" aria-hidden="true"></span>
    </slot>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useInView } from '@/composables/useInView'

const props = withDefaults(defineProps<{
  /** Immediate or reactive URL. Used after visibility (and optional loader). */
  src?: string | null
  alt?: string
  rootMargin?: string
  /**
   * Optional async prepare step (e.g. resolve signed URL). Called once when
   * the tile first enters view. May return a URL override.
   */
  load?: (() => Promise<string | void | null | undefined>) | null
}>(), {
  src: '',
  alt: '',
  rootMargin: '240px 0px',
  load: null,
})

const emit = defineEmits<{
  (event: 'error', cause?: unknown): void
  (event: 'load'): void
  (event: 'visible'): void
}>()

const { target, inView } = useInView({ rootMargin: props.rootMargin })
const displaySrc = ref('')
const failed = ref(false)
const loading = ref(false)
let loadToken = 0

async function activate() {
  if (failed.value || loading.value) return
  const token = ++loadToken
  loading.value = true
  emit('visible')
  try {
    let next = String(props.src || '').trim()
    if (props.load) {
      const resolved = await props.load()
      if (token !== loadToken) return
      if (resolved) next = String(resolved).trim()
      else next = String(props.src || '').trim()
    }
    if (token !== loadToken) return
    if (!next) {
      failed.value = true
      emit('error')
      return
    }
    displaySrc.value = next
  } catch (cause) {
    if (token !== loadToken) return
    failed.value = true
    emit('error', cause)
  } finally {
    if (token === loadToken) loading.value = false
  }
}

function handleError(event: Event) {
  failed.value = true
  emit('error', event)
}

watch(inView, (visible) => {
  if (visible) void activate()
}, { immediate: true })

watch(() => props.src, (value) => {
  if (!inView.value || failed.value) return
  const next = String(value || '').trim()
  if (next && next !== displaySrc.value) displaySrc.value = next
})
</script>

<style scoped>
.lazy-image {
  position: relative;
  display: block;
  width: 100%;
  height: 100%;
  overflow: hidden;
  background: #f3f4f6;
}

:global(.dark) .lazy-image {
  background: #030712;
}

.lazy-image img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.lazy-image__placeholder,
.lazy-image__fallback {
  display: block;
  width: 100%;
  height: 100%;
  min-height: inherit;
  background: linear-gradient(90deg, rgba(229, 231, 235, 0.55), rgba(243, 244, 246, 0.9), rgba(229, 231, 235, 0.55));
  background-size: 200% 100%;
  animation: lazy-image-shimmer 1.2s ease-in-out infinite;
}

:global(.dark) .lazy-image__placeholder,
:global(.dark) .lazy-image__fallback {
  background: linear-gradient(90deg, rgba(31, 41, 55, 0.7), rgba(17, 24, 39, 0.95), rgba(31, 41, 55, 0.7));
  background-size: 200% 100%;
}

@keyframes lazy-image-shimmer {
  0% { background-position: 100% 0; }
  100% { background-position: -100% 0; }
}

@media (prefers-reduced-motion: reduce) {
  .lazy-image__placeholder,
  .lazy-image__fallback {
    animation: none;
  }
}
</style>
