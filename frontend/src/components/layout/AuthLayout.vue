<template>
  <div
    class="auth-shell"
    :class="{ 'is-ready': ready }"
    @pointermove="onPointerMove"
    @pointerleave="onPointerLeave"
  >
    <div class="auth-noise" aria-hidden="true"></div>
    <div class="auth-vignette" aria-hidden="true"></div>
    <div
      class="auth-cursor-glow"
      aria-hidden="true"
      :style="{ transform: `translate3d(${pointer.x}px, ${pointer.y}px, 0)` }"
    ></div>

    <div class="auth-fx" aria-hidden="true">
      <div class="auth-grid"></div>
      <div class="auth-scan"></div>
      <div class="auth-scan auth-scan--slow"></div>
      <div class="auth-slash"></div>
      <div class="auth-orb auth-orb--a"></div>
      <div class="auth-orb auth-orb--b"></div>
      <div class="auth-rain">
        <span v-for="n in 12" :key="`rain-${n}`" :style="{ '--r': n }"></span>
      </div>
      <div class="auth-particles">
        <i v-for="n in 14" :key="`p-${n}`" :style="{ '--p': n }"></i>
      </div>
      <svg class="auth-wires" viewBox="0 0 1200 700" preserveAspectRatio="none">
        <path class="auth-wire auth-wire--a" d="M40 620 C220 520, 340 420, 520 380 S860 300, 1160 120" />
        <path class="auth-wire auth-wire--b" d="M80 680 C300 600, 420 500, 640 460 S940 380, 1180 220" />
        <circle class="auth-packet auth-packet--a" r="4" cx="0" cy="0">
          <animateMotion dur="5.5s" repeatCount="indefinite" path="M40 620 C220 520, 340 420, 520 380 S860 300, 1160 120" />
        </circle>
        <circle class="auth-packet auth-packet--b" r="3.5" cx="0" cy="0">
          <animateMotion dur="7s" repeatCount="indefinite" path="M80 680 C300 600, 420 500, 640 460 S940 380, 1180 220" />
        </circle>
      </svg>
    </div>

    <header class="auth-header">
      <div class="auth-header-beam" aria-hidden="true"></div>
      <nav class="auth-nav">
        <router-link to="/" class="auth-brand-link" :aria-label="siteName">
          <span class="auth-logo">
            <span class="auth-logo-ring" aria-hidden="true"></span>
            <img :src="siteLogo || '/logo.svg'" alt="" />
          </span>
          <span class="auth-brand-meta">
            <span class="auth-brand-text">{{ siteName }}</span>
            <span class="auth-brand-sub">{{ t('home.authBrandSub') }}</span>
          </span>
        </router-link>

        <div class="auth-status-chip" aria-hidden="true">
          <span class="auth-live-dot"></span>
          <span>{{ t('home.authChannel') }}</span>
        </div>

        <div class="auth-tools">
          <div class="auth-locale">
            <LocaleSwitcher />
          </div>
          <button
            type="button"
            class="auth-tool-button"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            :aria-label="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <span class="auth-tool-sheen" aria-hidden="true"></span>
            <Icon :name="isDark ? 'sun' : 'moon'" size="md" />
          </button>
          <router-link to="/" class="auth-home-chip">
            <span>{{ t('home.authHome') }}</span>
            <Icon name="arrowRight" size="sm" />
          </router-link>
        </div>
      </nav>
    </header>

    <main class="auth-main">
      <div
        class="auth-panel-stage"
        :style="{ transform: `translate3d(${parallax.x}px, ${parallax.y}px, 0)` }"
      >
        <section class="auth-panel" :aria-label="siteName">
        <div class="auth-panel-corners" aria-hidden="true">
          <i></i><i></i><i></i><i></i>
        </div>
        <div class="auth-panel-scan" aria-hidden="true"></div>

        <div v-if="settingsLoaded" class="auth-intro">
          <div class="auth-panel-chrome" aria-hidden="true">
            <span></span><span></span><span></span>
          </div>
          <p class="auth-panel-code">{{ t('home.authPanelCode') }}</p>
          <router-link to="/" class="auth-brand-logo" tabindex="-1" aria-hidden="true">
            <img :src="siteLogo || '/logo.svg'" alt="" />
          </router-link>
          <h2 class="auth-site-name">{{ siteName }}</h2>
          <p class="auth-subtitle">{{ siteSubtitle }}</p>
        </div>

        <div class="auth-form-surface">
          <slot />
        </div>

        <div v-if="$slots.footer" class="auth-footer-links">
          <slot name="footer" />
        </div>
      </section>
      </div>
    </main>

    <footer class="auth-footer">
      <span class="auth-footer-mark">&copy; {{ currentYear }} {{ siteName }}</span>
      <span class="auth-footer-meta" aria-hidden="true">
        <span class="auth-live-dot"></span>
        {{ t('home.authSecureSession') }}
      </span>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import { sanitizeUrl } from '@/utils/url'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const appStore = useAppStore()
const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteLogo = computed(() =>
  sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', {
    allowRelative: true,
    allowDataUrl: true
  })
)
const siteSubtitle = computed(
  () =>
    appStore.cachedPublicSettings?.site_subtitle?.trim() ||
    'Subscription to API Conversion Platform'
)
const settingsLoaded = computed(() => appStore.publicSettingsLoaded)
const currentYear = computed(() => new Date().getFullYear())
const isDark = ref(document.documentElement.classList.contains('dark'))
const ready = ref(false)
const pointer = reactive({ x: -200, y: -200 })
const parallax = reactive({ x: 0, y: 0 })

function onPointerMove(event: PointerEvent) {
  pointer.x = event.clientX
  pointer.y = event.clientY
  const nx = event.clientX / window.innerWidth - 0.5
  const ny = event.clientY / window.innerHeight - 0.5
  parallax.x = nx * -10
  parallax.y = ny * -8
}

function onPointerLeave() {
  parallax.x = 0
  parallax.y = 0
}

function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}

onMounted(() => {
  const savedTheme = localStorage.getItem('theme')
  isDark.value = savedTheme !== 'light'
  document.documentElement.classList.toggle('dark', isDark.value)
  if (!savedTheme) {
    localStorage.setItem('theme', 'dark')
  }
  appStore.fetchPublicSettings()
  requestAnimationFrame(() => {
    ready.value = true
  })
})
</script>

<style scoped>
.auth-shell {
  --auth-bg: #e8ecef;
  --auth-bg-deep: #cfd8dd;
  --auth-surface: rgba(255, 255, 255, 0.82);
  --auth-ink: #0d1b21;
  --auth-muted: #5a6c73;
  --auth-line: rgba(13, 27, 33, 0.14);
  --auth-accent: #067a6f;
  --auth-signal: #d4891a;
  --auth-action-ink: #04201c;
  --auth-header-bg: rgba(232, 236, 239, 0.78);
  position: relative;
  display: grid;
  min-height: 100dvh;
  grid-template-rows: auto 1fr auto;
  overflow-x: clip;
  color: var(--auth-ink);
  background:
    radial-gradient(900px 500px at 88% 8%, rgba(6, 122, 111, 0.18), transparent 55%),
    radial-gradient(700px 420px at 8% 80%, rgba(212, 137, 26, 0.14), transparent 50%),
    linear-gradient(155deg, var(--auth-bg), var(--auth-bg-deep));
  font-family: 'Sora', ui-sans-serif, system-ui, sans-serif;
}

:global(html.dark .auth-shell) {
  --auth-bg: #081116;
  --auth-bg-deep: #050b0e;
  --auth-surface: rgba(14, 24, 29, 0.86);
  --auth-ink: #e8f1f4;
  --auth-muted: #90a2a9;
  --auth-line: rgba(232, 241, 244, 0.12);
  --auth-accent: #2fd0bc;
  --auth-signal: #efb143;
  --auth-action-ink: #04201c;
  --auth-header-bg: rgba(8, 17, 22, 0.78);
}

.auth-noise,
.auth-vignette,
.auth-fx,
.auth-cursor-glow {
  pointer-events: none;
  position: fixed;
  inset: 0;
  z-index: 0;
}

.auth-noise {
  opacity: 0.05;
  background-image: url("data:image/svg+xml,%3Csvg viewBox='0 0 160 160' xmlns='http://www.w3.org/2000/svg'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.85' numOctaves='3' stitchTiles='stitch'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23n)'/%3E%3C/svg%3E");
}

.auth-vignette {
  background: radial-gradient(circle at center, transparent 42%, rgba(0, 0, 0, 0.2) 100%);
}

.auth-cursor-glow {
  inset: auto;
  width: 280px;
  height: 280px;
  margin: -140px 0 0 -140px;
  border-radius: 50%;
  background: radial-gradient(circle, color-mix(in srgb, var(--auth-accent) 22%, transparent), transparent 70%);
  opacity: 0.7;
  transition: opacity 200ms ease;
  will-change: transform;
}

.auth-fx {
  overflow: hidden;
}

.auth-grid,
.auth-scan,
.auth-slash,
.auth-orb,
.auth-rain,
.auth-particles,
.auth-wires {
  position: absolute;
  inset: 0;
}

.auth-grid {
  background-image:
    linear-gradient(var(--auth-line) 1px, transparent 1px),
    linear-gradient(90deg, var(--auth-line) 1px, transparent 1px);
  background-size: 56px 56px;
  mask-image: radial-gradient(circle at 50% 40%, black, transparent 72%);
  transform: rotate(-6deg) scale(1.18);
  animation: authGridDrift 18s linear infinite;
}

.auth-scan {
  inset: auto;
  left: 0;
  right: 0;
  height: 18%;
  background: linear-gradient(180deg, transparent, color-mix(in srgb, var(--auth-accent) 18%, transparent), transparent);
  animation: authScan 5.6s ease-in-out infinite;
}

.auth-scan--slow {
  height: 12%;
  opacity: 0.55;
  animation-duration: 9s;
  animation-delay: 1.4s;
  background: linear-gradient(180deg, transparent, color-mix(in srgb, var(--auth-signal) 16%, transparent), transparent);
}

.auth-slash {
  top: -20%;
  right: -10%;
  left: auto;
  bottom: auto;
  width: 42vw;
  height: 140%;
  background: linear-gradient(180deg, color-mix(in srgb, var(--auth-accent) 16%, transparent), transparent 70%);
  clip-path: polygon(35% 0, 100% 0, 65% 100%, 0 100%);
  animation: authSlashPulse 6s ease-in-out infinite;
}

.auth-orb {
  inset: auto;
  border-radius: 50%;
  filter: blur(2px);
}

.auth-orb--a {
  top: 12%;
  right: 10%;
  width: 220px;
  height: 220px;
  background: radial-gradient(circle, color-mix(in srgb, var(--auth-accent) 42%, transparent), transparent 70%);
  animation: authFlare 7s ease-in-out infinite;
}

.auth-orb--b {
  bottom: 12%;
  left: 8%;
  width: 160px;
  height: 160px;
  background: radial-gradient(circle, color-mix(in srgb, var(--auth-signal) 38%, transparent), transparent 70%);
  animation: authFlare 8.5s ease-in-out infinite reverse;
}

.auth-rain {
  overflow: hidden;
  opacity: 0.32;
}

.auth-rain span {
  position: absolute;
  top: -20%;
  left: calc(var(--r) * 8%);
  width: 1px;
  height: 16%;
  background: linear-gradient(180deg, transparent, var(--auth-accent), transparent);
  animation: authRainFall calc(3s + var(--r) * 0.25s) linear infinite;
  animation-delay: calc(var(--r) * -0.35s);
}

.auth-particles {
  overflow: hidden;
}

.auth-particles i {
  position: absolute;
  left: calc((var(--p) * 53) % 100 * 1%);
  bottom: -8%;
  width: 4px;
  height: 4px;
  border-radius: 50%;
  background: var(--auth-accent);
  opacity: 0.5;
  animation: authParticleRise calc(7s + var(--p) * 0.35s) linear infinite;
  animation-delay: calc(var(--p) * -0.45s);
  box-shadow: 0 0 10px color-mix(in srgb, var(--auth-accent) 60%, transparent);
}

.auth-wires {
  width: 100%;
  height: 100%;
  opacity: 0.5;
}

.auth-wire {
  fill: none;
  stroke: color-mix(in srgb, var(--auth-accent) 55%, transparent);
  stroke-width: 1.2;
  stroke-dasharray: 8 12;
  animation: authWireDash 18s linear infinite;
}

.auth-wire--b {
  stroke: color-mix(in srgb, var(--auth-signal) 55%, transparent);
  animation-duration: 24s;
  animation-direction: reverse;
}

.auth-packet {
  fill: var(--auth-accent);
  filter: drop-shadow(0 0 6px color-mix(in srgb, var(--auth-accent) 70%, transparent));
}

.auth-packet--b {
  fill: var(--auth-signal);
}

.auth-header,
.auth-main,
.auth-footer {
  position: relative;
  z-index: 1;
}

.auth-header {
  position: sticky;
  top: 0;
  z-index: 20;
  padding: 14px clamp(18px, 4vw, 56px);
  border-bottom: 1px solid var(--auth-line);
  background:
    linear-gradient(180deg, color-mix(in srgb, var(--auth-accent) 10%, transparent), transparent 72%),
    var(--auth-header-bg);
  backdrop-filter: blur(16px) saturate(1.15);
}

.auth-header-beam {
  position: absolute;
  left: 8%;
  right: 8%;
  bottom: -1px;
  height: 2px;
  background: linear-gradient(90deg, transparent, var(--auth-accent), var(--auth-signal), var(--auth-accent), transparent);
  background-size: 220% 100%;
  animation: authHeaderBeam 4.5s linear infinite;
}

.auth-nav {
  display: grid;
  grid-template-columns: 1fr auto 1fr;
  align-items: center;
  gap: 16px;
  max-width: 1280px;
  margin: 0 auto;
}

.auth-brand-link,
.auth-tools,
.auth-home-chip {
  display: flex;
  align-items: center;
}

.auth-brand-link {
  min-width: 0;
  gap: 12px;
  justify-self: start;
  color: var(--auth-ink);
  text-decoration: none;
}

.auth-logo {
  position: relative;
  display: grid;
  width: 44px;
  height: 44px;
  place-items: center;
  border: 1px solid var(--auth-line);
  border-radius: 12px;
  background: var(--auth-surface);
  transition: border-color 220ms ease, transform 220ms ease, box-shadow 220ms ease;
}

.auth-logo-ring {
  position: absolute;
  inset: -5px;
  border: 1px dashed color-mix(in srgb, var(--auth-accent) 55%, transparent);
  border-radius: 16px;
  opacity: 0;
  transition: opacity 220ms ease;
}

.auth-logo img {
  width: 70%;
  height: 70%;
  object-fit: contain;
  transition: transform 280ms ease;
}

.auth-brand-link:hover .auth-logo,
.auth-brand-link:focus-visible .auth-logo {
  border-color: var(--auth-accent);
  transform: translateY(-1px);
  box-shadow: 0 0 0 4px color-mix(in srgb, var(--auth-accent) 16%, transparent);
}

.auth-brand-link:hover .auth-logo-ring,
.auth-brand-link:focus-visible .auth-logo-ring {
  opacity: 1;
  animation: authLogoSpin 6s linear infinite;
}

.auth-brand-link:hover .auth-logo img {
  transform: scale(1.08);
}

.auth-brand-meta {
  display: grid;
  gap: 2px;
}

.auth-brand-text {
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 15px;
  font-weight: 700;
  letter-spacing: 0.04em;
}

.auth-brand-sub {
  color: var(--auth-muted);
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.18em;
}

.auth-status-chip {
  display: inline-flex;
  align-items: center;
  justify-self: center;
  gap: 8px;
  padding: 8px 14px;
  border: 1px solid color-mix(in srgb, var(--auth-accent) 35%, var(--auth-line));
  border-radius: 999px;
  color: var(--auth-accent);
  background: color-mix(in srgb, var(--auth-accent) 10%, var(--auth-surface));
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.1em;
}

.auth-live-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--auth-accent);
  box-shadow: 0 0 0 0 color-mix(in srgb, var(--auth-accent) 55%, transparent);
  animation: authLive 1.8s ease-out infinite;
}

.auth-tools {
  gap: 8px;
  justify-self: end;
}

.auth-tool-button {
  position: relative;
  display: grid;
  width: 42px;
  height: 42px;
  place-items: center;
  overflow: hidden;
  border: 1px solid var(--auth-line);
  border-radius: 12px;
  color: var(--auth-ink);
  background: var(--auth-surface);
  transition: border-color 200ms ease, transform 200ms ease, color 200ms ease, box-shadow 200ms ease;
}

.auth-tool-sheen {
  position: absolute;
  inset: 0;
  background: linear-gradient(120deg, transparent 30%, color-mix(in srgb, var(--auth-accent) 35%, transparent) 50%, transparent 70%);
  transform: translateX(-120%);
  transition: transform 420ms ease;
}

.auth-tool-button:hover,
.auth-tool-button:focus-visible {
  border-color: var(--auth-accent);
  color: var(--auth-accent);
  transform: translateY(-2px);
  box-shadow: 0 8px 20px color-mix(in srgb, var(--auth-accent) 18%, transparent);
}

.auth-tool-button:hover .auth-tool-sheen,
.auth-tool-button:focus-visible .auth-tool-sheen {
  transform: translateX(120%);
}

.auth-home-chip {
  min-height: 42px;
  gap: 8px;
  padding: 0 14px;
  border-radius: 12px;
  color: var(--auth-action-ink);
  background: var(--auth-accent);
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-decoration: none;
  transition: transform 200ms ease, box-shadow 200ms ease;
}

.auth-home-chip:hover,
.auth-home-chip:focus-visible {
  transform: translateY(-2px);
  box-shadow: 0 10px 24px color-mix(in srgb, var(--auth-accent) 28%, transparent);
}

.auth-main {
  display: grid;
  place-items: center;
  padding: clamp(28px, 5vh, 56px) max(18px, env(safe-area-inset-left)) 40px;
}

.auth-panel-stage {
  width: min(100%, 520px);
  will-change: transform;
  transition: transform 120ms ease-out;
}

.auth-panel {
  position: relative;
  width: 100%;
  padding: 22px 18px 18px;
  border: 1px solid var(--auth-line);
  border-radius: 22px;
  background: color-mix(in srgb, var(--auth-surface) 92%, transparent);
  backdrop-filter: blur(14px);
  overflow: hidden;
  opacity: 0;
  transform: translateY(18px);
  transition: opacity 700ms ease, transform 700ms ease, border-color 240ms ease, box-shadow 240ms ease;
}

.auth-shell.is-ready .auth-panel {
  opacity: 1;
  transform: translateY(0);
}

.auth-panel:hover {
  border-color: color-mix(in srgb, var(--auth-accent) 45%, var(--auth-line));
  box-shadow: 0 20px 48px color-mix(in srgb, var(--auth-ink) 10%, transparent);
}

.auth-panel-corners i {
  position: absolute;
  width: 14px;
  height: 14px;
  border-color: var(--auth-accent);
  border-style: solid;
  opacity: 0.7;
}

.auth-panel-corners i:nth-child(1) { top: 10px; left: 10px; border-width: 2px 0 0 2px; }
.auth-panel-corners i:nth-child(2) { top: 10px; right: 10px; border-width: 2px 2px 0 0; }
.auth-panel-corners i:nth-child(3) { bottom: 10px; left: 10px; border-width: 0 0 2px 2px; }
.auth-panel-corners i:nth-child(4) { bottom: 10px; right: 10px; border-width: 0 2px 2px 0; }

.auth-panel-scan {
  position: absolute;
  left: 0;
  right: 0;
  height: 26%;
  background: linear-gradient(180deg, transparent, color-mix(in srgb, var(--auth-accent) 10%, transparent), transparent);
  animation: authFooterScan 7s ease-in-out infinite;
}

.auth-intro {
  position: relative;
  z-index: 1;
  margin-bottom: 18px;
  text-align: center;
}

.auth-panel-chrome {
  display: flex;
  gap: 6px;
  margin-bottom: 12px;
  justify-content: center;
}

.auth-panel-chrome span {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: color-mix(in srgb, var(--auth-muted) 45%, transparent);
}

.auth-panel-chrome span:first-child { background: #ff6b6b; }
.auth-panel-chrome span:nth-child(2) { background: var(--auth-signal); }
.auth-panel-chrome span:nth-child(3) { background: var(--auth-accent); }

.auth-panel-code {
  margin: 0 0 12px;
  color: var(--auth-accent);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  letter-spacing: 0.04em;
}

.auth-brand-logo {
  display: grid;
  width: 56px;
  height: 56px;
  margin: 0 auto 12px;
  place-items: center;
  overflow: hidden;
  border: 1px solid var(--auth-line);
  border-radius: 14px;
  background: color-mix(in srgb, var(--auth-bg) 50%, transparent);
}

.auth-brand-logo img {
  width: 70%;
  height: 70%;
  object-fit: contain;
}

.auth-site-name {
  margin: 0;
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 22px;
  font-weight: 700;
  letter-spacing: -0.02em;
}

.auth-subtitle {
  margin: 8px 0 0;
  color: var(--auth-muted);
  font-size: 13px;
  line-height: 1.55;
}

.auth-form-surface {
  position: relative;
  z-index: 1;
  padding: clamp(18px, 3vw, 24px);
  border: 1px solid var(--auth-line);
  border-radius: 16px;
  background: color-mix(in srgb, var(--auth-bg) 35%, transparent);
}

.auth-footer-links {
  position: relative;
  z-index: 1;
  margin-top: 18px;
  text-align: center;
  font-size: 14px;
}

.auth-footer {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 18px;
  flex-wrap: wrap;
  padding: 18px clamp(16px, 4vw, 40px) 28px;
  color: var(--auth-muted);
  font-size: 12px;
}

.auth-footer-mark {
  font-family: 'Oxanium', 'Sora', sans-serif;
  letter-spacing: 0.04em;
}

.auth-footer-meta {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 11px;
  letter-spacing: 0.12em;
}

/* Slot content theming */
.auth-form-surface :deep(.auth-hero-code) {
  margin: 0 0 8px;
  color: var(--auth-accent);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  letter-spacing: 0.04em;
}

.auth-form-surface :deep(.auth-hero-title) {
  margin: 0;
  color: var(--auth-ink);
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 1.65rem;
  font-weight: 700;
  letter-spacing: -0.02em;
}

.auth-form-surface :deep(.auth-hero-sub) {
  margin: 8px 0 0;
  color: var(--auth-muted);
  font-size: 0.875rem;
  line-height: 1.5;
}

.auth-form-surface :deep(h1),
.auth-form-surface :deep(h2) {
  font-family: 'Oxanium', 'Sora', sans-serif !important;
  letter-spacing: -0.02em;
}

.auth-form-surface :deep(.input-label) {
  color: var(--auth-muted);
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 12px;
  letter-spacing: 0.06em;
  text-transform: uppercase;
}

.auth-form-surface :deep(.input) {
  border-color: var(--auth-line);
  background: color-mix(in srgb, var(--auth-surface) 80%, transparent);
  color: var(--auth-ink);
  border-radius: 12px;
  transition: border-color 180ms ease, box-shadow 180ms ease, transform 180ms ease;
}

.auth-form-surface :deep(.input:focus),
.auth-form-surface :deep(.input:focus-visible) {
  border-color: var(--auth-accent);
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--auth-accent) 18%, transparent);
}

.auth-form-surface :deep(.btn-primary),
.auth-form-surface :deep(button[type='submit'].btn),
.auth-form-surface :deep(button.btn-primary) {
  border-radius: 12px;
  background: var(--auth-accent) !important;
  color: var(--auth-action-ink) !important;
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-weight: 700;
  letter-spacing: 0.04em;
  transition: transform 200ms ease, box-shadow 200ms ease, filter 200ms ease;
}

.auth-form-surface :deep(.btn-primary:hover),
.auth-form-surface :deep(button[type='submit'].btn:hover),
.auth-form-surface :deep(button.btn-primary:hover) {
  filter: brightness(1.06);
  transform: translateY(-2px);
  box-shadow: 0 12px 26px color-mix(in srgb, var(--auth-accent) 30%, transparent);
}

.auth-form-surface :deep(a) {
  color: var(--auth-accent);
  transition: color 160ms ease;
}

.auth-form-surface :deep(a:hover) {
  color: color-mix(in srgb, var(--auth-accent) 75%, var(--auth-ink));
}

.auth-footer-links :deep(a) {
  color: var(--auth-accent);
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-weight: 600;
  letter-spacing: 0.03em;
  text-decoration: none;
}

.auth-footer-links :deep(a:hover) {
  text-decoration: underline;
}

@keyframes authGridDrift {
  from { transform: rotate(-6deg) scale(1.18) translate3d(0, 0, 0); }
  to { transform: rotate(-6deg) scale(1.18) translate3d(-56px, -56px, 0); }
}

@keyframes authScan {
  0%, 100% { top: -18%; opacity: 0.2; }
  40% { opacity: 0.7; }
  100% { top: 100%; opacity: 0.1; }
}

@keyframes authFlare {
  0%, 100% { opacity: 0.5; transform: scale(1); }
  50% { opacity: 0.9; transform: scale(1.08); }
}

@keyframes authSlashPulse {
  0%, 100% { opacity: 0.4; }
  50% { opacity: 0.8; }
}

@keyframes authRainFall {
  from { transform: translateY(0); opacity: 0; }
  15% { opacity: 0.9; }
  to { transform: translateY(140vh); opacity: 0; }
}

@keyframes authParticleRise {
  from { transform: translate3d(0, 0, 0) scale(0.6); opacity: 0; }
  20% { opacity: 0.75; }
  to { transform: translate3d(20px, -110vh, 0) scale(1.2); opacity: 0; }
}

@keyframes authWireDash {
  from { stroke-dashoffset: 0; }
  to { stroke-dashoffset: -240; }
}

@keyframes authHeaderBeam {
  from { background-position: 0% 0; }
  to { background-position: 220% 0; }
}

@keyframes authLogoSpin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

@keyframes authLive {
  0% { box-shadow: 0 0 0 0 color-mix(in srgb, var(--auth-accent) 55%, transparent); }
  70% { box-shadow: 0 0 0 12px transparent; }
  100% { box-shadow: 0 0 0 0 transparent; }
}

@keyframes authFooterScan {
  0%, 100% { top: -30%; opacity: 0.2; }
  45% { opacity: 0.6; }
  100% { top: 110%; opacity: 0.15; }
}

.auth-locale :deep(button) {
  min-height: 42px;
  border: 1px solid var(--auth-line);
  border-radius: 12px;
  color: var(--auth-ink);
  background: var(--auth-surface);
  transition: border-color 200ms ease, color 200ms ease, transform 200ms ease;
}

.auth-locale :deep(button:hover),
.auth-locale :deep(button:focus-visible) {
  border-color: var(--auth-accent);
  color: var(--auth-accent);
  transform: translateY(-2px);
}

@media (max-width: 720px) {
  .auth-nav { grid-template-columns: 1fr auto; }
  .auth-status-chip { display: none; }
  .auth-locale { display: none; }
  .auth-home-chip span { display: none; }
  .auth-cursor-glow,
  .auth-rain,
  .auth-wires {
    display: none;
  }
}

@media (max-width: 480px) {
  .auth-header { padding: 12px 14px; }
  .auth-brand-meta { display: none; }
  .auth-main { place-items: start center; padding: 20px 0 28px; }
  .auth-panel-stage { width: 100%; }
  .auth-panel {
    width: 100%;
    border-radius: 0;
    border-inline: 0;
  }
  .auth-form-surface {
    border-radius: 14px;
  }
  .auth-particles {
    opacity: 0.55;
  }
}

@media (prefers-reduced-motion: reduce) {
  .auth-shell *,
  .auth-shell *::before,
  .auth-shell *::after {
    animation: none !important;
    transition: none !important;
  }

  .auth-panel {
    opacity: 1;
    transform: none;
  }

  .auth-panel-stage {
    transform: none !important;
  }

  .auth-cursor-glow,
  .auth-rain,
  .auth-particles,
  .auth-wires {
    display: none;
  }
}
</style>
