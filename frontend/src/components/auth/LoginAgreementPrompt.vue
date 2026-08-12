<template>
  <div
    v-if="mode === 'checkbox' && documents.length > 0"
    class="agree-shell agree-check"
  >
    <div class="agree-check-row">
      <input
        id="login-agreement-consent"
        type="checkbox"
        :checked="accepted"
        class="agree-checkbox"
        @change="handleCheckboxChange"
      />
      <div class="min-w-0 flex-1">
        <p class="agree-check-text">
          <label for="login-agreement-consent" class="agree-check-label">
            {{ t('legal.loginAgreementPrompt.checkboxPrefix') }}
          </label>
          <template v-for="(doc, index) in documents" :key="doc.id || doc.title">
            <RouterLink
              :to="documentRoute(doc)"
              target="_blank"
              rel="noopener noreferrer"
              class="agree-inline-link"
            >
              {{ doc.title }}
            </RouterLink>
            <span v-if="index < documents.length - 1">{{ t('legal.loginAgreementPrompt.documentSeparator') }}</span>
          </template>
        </p>
      </div>
    </div>
  </div>

  <div
    v-else-if="!accepted && documents.length > 0"
    class="agree-shell agree-notice"
  >
    <div class="agree-notice-inner">
      <Icon name="shield" size="sm" class="agree-notice-icon" />
      <div class="min-w-0 flex-1">
        <p class="agree-notice-title">{{ t('legal.loginAgreementPrompt.noticeTitle') }}</p>
        <p class="agree-notice-desc">
          {{ t('legal.loginAgreementPrompt.noticeDescription') }}
        </p>
      </div>
      <button type="button" class="agree-btn agree-btn-mini" @click="emit('open')">
        <span class="agree-btn-sheen" aria-hidden="true"></span>
        <span>{{ t('legal.loginAgreementPrompt.viewTerms') }}</span>
      </button>
    </div>
  </div>

  <Teleport to="body">
    <Transition name="agreement-fade">
      <div
        v-if="dialogVisible"
        class="agree-shell agree-overlay"
        role="dialog"
        aria-modal="true"
        :aria-label="t('legal.loginAgreementPrompt.dialogTitle')"
      >
        <div class="agree-frame">
          <div class="agree-dialog">
            <div class="agree-dialog-corners" aria-hidden="true">
              <i></i><i></i><i></i><i></i>
            </div>

            <header class="agree-header">
              <div class="agree-header-chrome" aria-hidden="true">
                <span></span><span></span><span></span>
              </div>
              <div class="agree-header-row">
                <span class="agree-shield">
                  <span class="agree-shield-ring" aria-hidden="true"></span>
                  <Icon name="shield" size="md" />
                </span>
                <div class="min-w-0 flex-1">
                  <div class="agree-title-row">
                    <h2 class="agree-title">
                      {{ t('legal.loginAgreementPrompt.dialogTitle') }}
                    </h2>
                    <span v-if="updatedAt" class="agree-chip">{{ updatedAt }}</span>
                  </div>
                  <p class="agree-desc">
                    {{
                      t('legal.loginAgreementPrompt.dialogDescription', {
                        date: updatedAt || t('legal.loginAgreementPrompt.recently'),
                      })
                    }}
                  </p>
                </div>
              </div>
            </header>

            <div class="agree-body">
              <p class="agree-section-label">{{ t('legal.loginAgreementPrompt.relatedDocuments') }}</p>
              <div class="agree-docs">
                <RouterLink
                  v-for="(doc, index) in documents"
                  :key="doc.id || doc.title"
                  :to="documentRoute(doc)"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="agree-doc"
                >
                  <span class="agree-doc-sheen" aria-hidden="true"></span>
                  <span class="agree-doc-icon">
                    <Icon :name="documentIcon(index, doc.title)" size="sm" />
                  </span>
                  <span class="agree-doc-title">{{ doc.title }}</span>
                  <span class="agree-doc-arrow">
                    <Icon name="externalLink" size="sm" />
                  </span>
                </RouterLink>
              </div>
            </div>

            <footer class="agree-footer">
              <button type="button" class="agree-btn agree-btn-ghost" @click="emit('reject')">
                <span class="agree-btn-sheen" aria-hidden="true"></span>
                <span>{{ t('legal.loginAgreementPrompt.reject') }}</span>
              </button>
              <button type="button" class="agree-btn agree-btn-primary" @click="emit('accept')">
                <span class="agree-btn-sheen" aria-hidden="true"></span>
                <span>{{ t('legal.loginAgreementPrompt.accept') }}</span>
                <Icon name="arrowRight" size="sm" class="agree-btn-arrow" />
              </button>
            </footer>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { LoginAgreementDocument } from '@/types'

const { t } = useI18n()

const props = withDefaults(defineProps<{
  accepted: boolean
  documents: LoginAgreementDocument[]
  mode: 'modal' | 'checkbox' | string
  updatedAt?: string
  visible: boolean
}>(), {
  updatedAt: ''
})

const emit = defineEmits<{
  accept: []
  reject: []
  open: []
}>()

const dialogVisible = computed(() => props.visible && documents.value.length > 0)
const documents = computed(() => props.documents.filter((doc) => doc.title.trim()))
const updatedAt = computed(() => props.updatedAt || '')
const accepted = computed(() => props.accepted)
const mode = computed(() => props.mode === 'checkbox' ? 'checkbox' : 'modal')

function documentRoute(doc: LoginAgreementDocument) {
  return {
    name: 'LegalDocument',
    params: {
      documentId: doc.id || doc.title,
    },
  }
}

function handleCheckboxChange(event: Event): void {
  const checked = (event.target as HTMLInputElement).checked
  if (checked) {
    emit('accept')
  } else {
    emit('reject')
  }
}

function documentIcon(index: number, title: string): 'document' | 'shield' | 'globe' | 'cog' {
  const normalizedTitle = title.toLowerCase()
  if (
    normalizedTitle.includes('policy') ||
    normalizedTitle.includes('privacy') ||
    title.includes('政策') ||
    title.includes('隐私')
  ) {
    return 'shield'
  }
  if (
    normalizedTitle.includes('country') ||
    normalizedTitle.includes('region') ||
    title.includes('国家') ||
    title.includes('地区')
  ) {
    return 'globe'
  }
  if (index === 3) {
    return 'cog'
  }
  return 'document'
}
</script>

<style scoped>
.agree-shell {
  --agree-bg: #e8ecef;
  --agree-surface: #f4f7f8;
  --agree-ink: #0d1b21;
  --agree-muted: #5a6c73;
  --agree-line: rgba(13, 27, 33, 0.14);
  --agree-accent: #067a6f;
  --agree-signal: #d4891a;
  --agree-action-ink: #04201c;
  font-family: 'Sora', ui-sans-serif, system-ui, sans-serif;
  color: var(--agree-ink);
}

:global(html.dark .agree-shell) {
  --agree-bg: #081116;
  --agree-surface: #0e181d;
  --agree-ink: #e8f1f4;
  --agree-muted: #90a2a9;
  --agree-line: rgba(232, 241, 244, 0.12);
  --agree-accent: #2fd0bc;
  --agree-signal: #efb143;
  --agree-action-ink: #04201c;
}

/* Inline checkbox mode */
.agree-check-row {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}

.agree-checkbox {
  margin-top: 3px;
  width: 16px;
  height: 16px;
  flex-shrink: 0;
  accent-color: var(--agree-accent);
  cursor: pointer;
}

.agree-check-text {
  margin: 0;
  color: var(--agree-muted);
  font-size: 13px;
  line-height: 1.55;
}

.agree-check-label {
  color: var(--agree-ink);
  cursor: pointer;
}

.agree-inline-link {
  color: var(--agree-accent);
  font-weight: 600;
  text-underline-offset: 3px;
  transition: color 160ms ease, text-decoration-color 160ms ease;
}

.agree-inline-link:hover {
  color: color-mix(in srgb, var(--agree-accent) 70%, var(--agree-ink));
  text-decoration: underline;
}

/* Compact notice banner */
.agree-notice {
  position: relative;
  overflow: hidden;
  border: 1px solid color-mix(in srgb, var(--agree-accent) 35%, var(--agree-line));
  border-radius: 14px;
  background:
    linear-gradient(120deg, color-mix(in srgb, var(--agree-accent) 12%, transparent), transparent 55%),
    color-mix(in srgb, var(--agree-surface) 88%, transparent);
}

.agree-notice-inner {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 12px 14px;
}

.agree-notice-icon {
  margin-top: 2px;
  flex-shrink: 0;
  color: var(--agree-accent);
}

.agree-notice-title {
  margin: 0;
  color: var(--agree-ink);
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 13px;
  font-weight: 700;
}

.agree-notice-desc {
  margin: 4px 0 0;
  color: var(--agree-muted);
  font-size: 12px;
  line-height: 1.5;
}

/* Overlay + flowing border frame */
.agree-overlay {
  position: fixed;
  inset: 0;
  z-index: 140;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow-y: auto;
  padding: 16px;
  background: rgba(5, 11, 14, 0.62);
  backdrop-filter: blur(6px);
}

.agree-frame {
  position: relative;
  width: min(100%, 600px);
  padding: 2px;
  overflow: hidden;
  border-radius: 22px;
  isolation: isolate;
}

/* 仅露出 2px 边框环上的流光，旋转层被裁切，不会铺满背景 */
.agree-frame::before {
  content: '';
  position: absolute;
  inset: -60%;
  z-index: 0;
  background: conic-gradient(
    from 0deg,
    transparent 0deg,
    transparent 55deg,
    var(--agree-accent) 95deg,
    var(--agree-signal) 125deg,
    var(--agree-accent) 155deg,
    transparent 195deg,
    transparent 360deg
  );
  animation: agreeBorderSpin 2.8s linear infinite;
}

.agree-frame::after {
  content: '';
  position: absolute;
  inset: 2px;
  z-index: 0;
  border-radius: 20px;
  background: var(--agree-surface);
}

.agree-dialog {
  position: relative;
  z-index: 1;
  overflow: hidden;
  border-radius: 20px;
  background: var(--agree-surface);
  color: var(--agree-ink);
  box-shadow: 0 24px 64px rgba(0, 0, 0, 0.28);
}

.agree-dialog-corners i {
  position: absolute;
  width: 14px;
  height: 14px;
  border-color: var(--agree-accent);
  border-style: solid;
  opacity: 0.75;
  pointer-events: none;
}

.agree-dialog-corners i:nth-child(1) { top: 12px; left: 12px; border-width: 2px 0 0 2px; }
.agree-dialog-corners i:nth-child(2) { top: 12px; right: 12px; border-width: 2px 2px 0 0; }
.agree-dialog-corners i:nth-child(3) { bottom: 12px; left: 12px; border-width: 0 0 2px 2px; }
.agree-dialog-corners i:nth-child(4) { bottom: 12px; right: 12px; border-width: 0 2px 2px 0; }

.agree-header {
  position: relative;
  z-index: 1;
  padding: 22px 22px 16px;
  border-bottom: 1px solid var(--agree-line);
}

.agree-header-chrome {
  display: flex;
  gap: 6px;
  margin-bottom: 14px;
}

.agree-header-chrome span {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: color-mix(in srgb, var(--agree-muted) 45%, transparent);
}

.agree-header-chrome span:first-child { background: #ff6b6b; }
.agree-header-chrome span:nth-child(2) { background: var(--agree-signal); }
.agree-header-chrome span:nth-child(3) { background: var(--agree-accent); }

.agree-header-row {
  display: flex;
  align-items: flex-start;
  gap: 14px;
}

.agree-shield {
  position: relative;
  display: grid;
  width: 48px;
  height: 48px;
  flex-shrink: 0;
  place-items: center;
  border: 1px solid color-mix(in srgb, var(--agree-accent) 40%, var(--agree-line));
  border-radius: 14px;
  color: var(--agree-accent);
  background: color-mix(in srgb, var(--agree-accent) 12%, transparent);
}

.agree-shield-ring {
  position: absolute;
  inset: -5px;
  border: 1px dashed color-mix(in srgb, var(--agree-accent) 50%, transparent);
  border-radius: 18px;
  animation: agreeLogoSpin 8s linear infinite;
}

.agree-title-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.agree-title {
  margin: 0;
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 1.25rem;
  font-weight: 700;
  letter-spacing: -0.02em;
}

.agree-chip {
  padding: 4px 10px;
  border: 1px solid var(--agree-line);
  border-radius: 999px;
  color: var(--agree-muted);
  background: color-mix(in srgb, var(--agree-bg) 45%, transparent);
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.04em;
}

.agree-desc {
  margin: 8px 0 0;
  color: var(--agree-muted);
  font-size: 13px;
  line-height: 1.6;
}

.agree-body {
  position: relative;
  z-index: 1;
  max-height: 52vh;
  overflow-y: auto;
  padding: 16px 22px 8px;
}

.agree-section-label {
  margin: 0 0 12px;
  color: var(--agree-accent);
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.14em;
  text-transform: uppercase;
}

.agree-docs {
  display: grid;
  grid-template-columns: 1fr;
  gap: 10px;
}

@media (min-width: 560px) {
  .agree-docs {
    grid-template-columns: 1fr 1fr;
  }
}

.agree-doc {
  position: relative;
  display: flex;
  min-height: 72px;
  align-items: center;
  gap: 12px;
  overflow: hidden;
  padding: 12px 14px;
  border: 1px solid var(--agree-line);
  border-radius: 14px;
  color: var(--agree-ink);
  background: color-mix(in srgb, var(--agree-bg) 40%, transparent);
  text-decoration: none;
  transition:
    transform 200ms ease,
    border-color 200ms ease,
    box-shadow 200ms ease,
    background 200ms ease;
}

.agree-doc-sheen {
  position: absolute;
  inset: 0;
  background: linear-gradient(
    120deg,
    transparent 30%,
    color-mix(in srgb, var(--agree-accent) 28%, transparent) 50%,
    transparent 70%
  );
  transform: translateX(-120%);
  transition: transform 420ms ease;
  pointer-events: none;
}

.agree-doc:hover,
.agree-doc:focus-visible {
  border-color: color-mix(in srgb, var(--agree-accent) 55%, var(--agree-line));
  background: color-mix(in srgb, var(--agree-accent) 10%, var(--agree-surface));
  transform: translateY(-3px);
  box-shadow: 0 12px 28px color-mix(in srgb, var(--agree-accent) 16%, transparent);
}

.agree-doc:hover .agree-doc-sheen,
.agree-doc:focus-visible .agree-doc-sheen {
  transform: translateX(120%);
}

.agree-doc-icon {
  display: grid;
  width: 40px;
  height: 40px;
  flex-shrink: 0;
  place-items: center;
  border: 1px solid var(--agree-line);
  border-radius: 12px;
  color: var(--agree-muted);
  background: color-mix(in srgb, var(--agree-surface) 80%, transparent);
  transition: color 180ms ease, border-color 180ms ease, background 180ms ease;
}

.agree-doc:hover .agree-doc-icon {
  color: var(--agree-accent);
  border-color: color-mix(in srgb, var(--agree-accent) 45%, var(--agree-line));
  background: color-mix(in srgb, var(--agree-accent) 12%, transparent);
}

.agree-doc-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 13px;
  font-weight: 700;
}

.agree-doc-arrow {
  display: grid;
  width: 28px;
  height: 28px;
  flex-shrink: 0;
  place-items: center;
  border-radius: 999px;
  color: var(--agree-muted);
  transition: color 180ms ease, background 180ms ease, transform 180ms ease;
}

.agree-doc:hover .agree-doc-arrow {
  color: var(--agree-accent);
  background: color-mix(in srgb, var(--agree-accent) 12%, transparent);
  transform: translateX(2px);
}

.agree-footer {
  position: relative;
  z-index: 1;
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
  padding: 16px 22px 20px;
  border-top: 1px solid var(--agree-line);
  background: color-mix(in srgb, var(--agree-bg) 35%, transparent);
}

.agree-btn {
  position: relative;
  display: inline-flex;
  min-height: 48px;
  align-items: center;
  justify-content: center;
  gap: 8px;
  overflow: hidden;
  padding: 0 16px;
  border-radius: 12px;
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 0.04em;
  cursor: pointer;
  transition:
    transform 200ms ease,
    box-shadow 200ms ease,
    border-color 200ms ease,
    background 200ms ease,
    color 200ms ease,
    filter 200ms ease;
}

.agree-btn-sheen {
  position: absolute;
  inset: 0;
  background: linear-gradient(
    120deg,
    transparent 28%,
    rgba(255, 255, 255, 0.28) 50%,
    transparent 72%
  );
  transform: translateX(-120%);
  transition: transform 420ms ease;
  pointer-events: none;
}

.agree-btn:hover .agree-btn-sheen,
.agree-btn:focus-visible .agree-btn-sheen {
  transform: translateX(120%);
}

.agree-btn-ghost {
  border: 1px solid var(--agree-line);
  color: var(--agree-ink);
  background: color-mix(in srgb, var(--agree-surface) 85%, transparent);
}

.agree-btn-ghost:hover,
.agree-btn-ghost:focus-visible {
  border-color: var(--agree-accent);
  color: var(--agree-accent);
  transform: translateY(-2px);
  box-shadow: 0 10px 24px color-mix(in srgb, var(--agree-accent) 14%, transparent);
}

.agree-btn-primary {
  border: 1px solid transparent;
  color: var(--agree-action-ink);
  background: var(--agree-accent);
  box-shadow: 0 8px 20px color-mix(in srgb, var(--agree-accent) 22%, transparent);
}

.agree-btn-primary:hover,
.agree-btn-primary:focus-visible {
  filter: brightness(1.08);
  transform: translateY(-2px);
  box-shadow: 0 14px 30px color-mix(in srgb, var(--agree-accent) 34%, transparent);
}

.agree-btn-primary:hover .agree-btn-arrow,
.agree-btn-primary:focus-visible .agree-btn-arrow {
  transform: translateX(3px);
}

.agree-btn-arrow {
  transition: transform 200ms ease;
}

.agree-btn-mini {
  flex-shrink: 0;
  min-height: 34px;
  padding: 0 12px;
  border: 1px solid transparent;
  color: var(--agree-action-ink);
  background: var(--agree-accent);
  font-size: 11px;
}

.agree-btn-mini:hover,
.agree-btn-mini:focus-visible {
  filter: brightness(1.08);
  transform: translateY(-2px);
  box-shadow: 0 10px 22px color-mix(in srgb, var(--agree-accent) 28%, transparent);
}

@keyframes agreeBorderSpin {
  to { transform: rotate(360deg); }
}

@keyframes agreeLogoSpin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

.agreement-fade-enter-active,
.agreement-fade-leave-active {
  transition: opacity 0.22s ease;
}

.agreement-fade-enter-from,
.agreement-fade-leave-to {
  opacity: 0;
}

.agreement-fade-enter-active .agree-frame,
.agreement-fade-leave-active .agree-frame {
  transition: transform 0.28s ease, opacity 0.22s ease;
}

.agreement-fade-enter-from .agree-frame,
.agreement-fade-leave-to .agree-frame {
  opacity: 0;
  transform: translateY(14px) scale(0.97);
}

@media (prefers-reduced-motion: reduce) {
  .agree-frame::before,
  .agree-shield-ring,
  .agree-btn-sheen,
  .agree-doc-sheen {
    animation: none !important;
    transition: none !important;
  }

  .agree-btn:hover,
  .agree-doc:hover {
    transform: none;
  }
}
</style>
