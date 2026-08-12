<template>
  <Teleport to="body">
    <Transition name="image-lightbox-fade">
      <div
        v-if="src"
        class="image-lightbox"
        role="dialog"
        aria-modal="true"
        :aria-label="alt || t('common.view')"
        @click.self="close"
        @keydown.esc.prevent="close"
      >
        <button type="button" class="image-lightbox__close" :title="t('common.close')" :aria-label="t('common.close')" @click="close">
          <Icon name="x" size="lg" />
        </button>
        <img :src="src" :alt="alt || ''" class="image-lightbox__img" @click.stop />
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{
  src: string
  alt?: string
}>()

const emit = defineEmits<{ (event: 'close'): void }>()
const { t } = useI18n()

function close() {
  emit('close')
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape' && props.src) close()
}

watch(
  () => props.src,
  (value) => {
    document.body.style.overflow = value ? 'hidden' : ''
  },
)

onMounted(() => window.addEventListener('keydown', onKeydown))
onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown)
  document.body.style.overflow = ''
})
</script>

<style scoped>
.image-lightbox {
  position: fixed;
  inset: 0;
  z-index: 100;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 1rem;
  background: rgba(0, 0, 0, 0.82);
}
.image-lightbox__close {
  position: absolute;
  top: 1rem;
  right: 1rem;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 2.5rem;
  height: 2.5rem;
  border-radius: 9999px;
  border: 0;
  background: rgba(0, 0, 0, 0.45);
  color: #fff;
  cursor: pointer;
}
.image-lightbox__close:hover {
  background: rgba(0, 0, 0, 0.7);
}
.image-lightbox__img {
  max-width: 92vw;
  max-height: 90vh;
  border-radius: 0.5rem;
  object-fit: contain;
  box-shadow: 0 20px 50px rgba(0, 0, 0, 0.45);
}
.image-lightbox-fade-enter-active,
.image-lightbox-fade-leave-active {
  transition: opacity 0.16s ease;
}
.image-lightbox-fade-enter-from,
.image-lightbox-fade-leave-to {
  opacity: 0;
}
</style>
