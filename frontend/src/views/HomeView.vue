<template>
  <!-- Custom Home Content: Full Page Mode -->
  <div v-if="hasHomeContent" class="min-h-screen bg-white dark:bg-dark-950">
    <iframe
      v-if="isHomeContentUrl && iframeHomeUrl"
      :src="iframeHomeUrl"
      class="h-screen w-full border-0"
      sandbox="allow-forms allow-scripts allow-popups"
      referrerpolicy="no-referrer"
      title="Custom home content"
    />
    <div v-else class="custom-home-content" v-html="sanitizedHomeContent"></div>
  </div>

  <!-- Compact Home Page -->
  <div
    v-else-if="compactHomeEnabled"
    data-testid="compact-home"
    class="flex min-h-screen flex-col bg-gray-50 text-gray-900 dark:bg-dark-950 dark:text-white"
  >
    <header class="border-b border-gray-200 px-4 py-4 sm:px-6 dark:border-dark-800">
      <nav class="mx-auto flex max-w-5xl flex-wrap items-center justify-between gap-3 sm:gap-4">
        <div class="flex min-w-0 flex-1 items-center gap-3">
          <img
            :src="siteLogo || '/logo.svg'"
            alt="Logo"
            class="h-9 w-9 shrink-0 rounded-lg object-contain"
          />
          <span class="min-w-0 truncate text-base font-semibold">{{ siteName }}</span>
        </div>
        <div class="flex max-w-full shrink-0 flex-wrap items-center justify-end gap-2">
          <LocaleSwitcher />
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
            :title="t('home.viewDocs')"
          >
            <Icon name="book" size="md" />
          </a>
          <button
            class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-gray-500 hover:bg-gray-100 dark:text-dark-400 dark:hover:bg-dark-800"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <Icon v-if="isDark" name="sun" size="md" />
            <Icon v-else name="moon" size="md" />
          </button>
          <router-link
            :to="isAuthenticated ? dashboardPath : '/login'"
            class="inline-flex min-h-10 shrink-0 items-center justify-center rounded-lg bg-gray-900 px-4 py-2 text-sm font-medium text-white hover:bg-gray-800 dark:bg-white dark:text-gray-900 dark:hover:bg-gray-200"
          >
            {{ isAuthenticated ? t('home.dashboard') : t('home.login') }}
          </router-link>
        </div>
      </nav>
    </header>

    <main class="flex min-w-0 flex-1 items-center justify-center px-4 py-16 sm:px-6">
      <div class="min-w-0 max-w-2xl text-center">
        <img
          :src="siteLogo || '/logo.svg'"
          alt="Logo"
          class="mx-auto mb-6 h-20 w-20 rounded-2xl object-contain"
        />
        <h1 class="[overflow-wrap:anywhere] text-3xl font-bold md:text-4xl">{{ siteName }}</h1>
        <p class="mt-4 whitespace-pre-wrap [overflow-wrap:anywhere] text-base text-gray-600 dark:text-dark-300">{{ siteSubtitle }}</p>
        <router-link
          :to="isAuthenticated ? dashboardPath : '/login'"
          class="mt-8 inline-flex min-h-10 items-center justify-center rounded-lg bg-primary-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-primary-700"
        >
          {{ isAuthenticated ? t('home.goToDashboard') : t('home.login') }}
        </router-link>
      </div>
    </main>

    <footer class="min-w-0 border-t border-gray-200 px-4 py-5 text-center text-sm text-gray-500 [overflow-wrap:anywhere] sm:px-6 dark:border-dark-800 dark:text-dark-400">
      &copy; {{ currentYear }} {{ siteName }}
    </footer>
  </div>

  <!-- Default Home Page -->
  <div
    v-else
    class="home-shell"
    :class="{ 'is-ready': ready }"
    @pointermove="onPointerMove"
    @pointerleave="onPointerLeave"
  >
    <div class="home-noise" aria-hidden="true"></div>
    <div class="home-vignette" aria-hidden="true"></div>
    <div
      class="home-cursor-glow"
      aria-hidden="true"
      :style="{ transform: `translate3d(${pointer.x}px, ${pointer.y}px, 0)` }"
    ></div>

    <header class="home-header">
      <div class="home-header-beam" aria-hidden="true"></div>
      <nav class="home-nav" :aria-label="siteName">
        <router-link to="/" class="home-brand" :aria-label="siteName">
          <span class="home-logo">
            <span class="home-logo-ring" aria-hidden="true"></span>
            <img :src="siteLogo || '/logo.svg'" alt="" />
          </span>
          <span class="home-brand-meta">
            <span class="home-brand-text">{{ siteName }}</span>
            <span class="home-brand-sub">API GATEWAY</span>
          </span>
        </router-link>

        <div class="home-status-chip" aria-hidden="true">
          <span class="home-live-dot"></span>
          <span>{{ t('home.signalLive') }}</span>
        </div>

        <div class="home-actions">
          <LocaleSwitcher />
          <a
            v-if="docUrl"
            :href="docUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="home-icon-button"
            :title="t('home.viewDocs')"
            :aria-label="t('home.viewDocs')"
          >
            <span class="home-icon-sheen" aria-hidden="true"></span>
            <Icon name="book" size="md" />
          </a>
          <button
            type="button"
            class="home-icon-button"
            :title="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            :aria-label="isDark ? t('home.switchToLight') : t('home.switchToDark')"
            @click="toggleTheme"
          >
            <span class="home-icon-sheen" aria-hidden="true"></span>
            <Icon :name="isDark ? 'sun' : 'moon'" size="md" />
          </button>
          <router-link :to="isAuthenticated ? dashboardPath : '/login'" class="home-session-link">
            <span class="home-session-glow" aria-hidden="true"></span>
            <span>{{ isAuthenticated ? t('home.dashboard') : t('home.login') }}</span>
            <Icon name="arrowRight" size="sm" class="home-session-arrow" />
          </router-link>
        </div>
      </nav>
    </header>

    <main>
      <section class="home-hero" aria-labelledby="home-brand-title">
        <div class="home-hero-bg" aria-hidden="true">
          <div class="home-grid"></div>
          <div class="home-scan"></div>
          <div class="home-scan home-scan--slow"></div>
          <div class="home-slash"></div>
          <div class="home-orb home-orb--a"></div>
          <div class="home-orb home-orb--b"></div>
          <div class="home-rain">
            <span v-for="n in 14" :key="`rain-${n}`" :style="{ '--r': n }"></span>
          </div>
          <div class="home-particles">
            <i v-for="n in 18" :key="`p-${n}`" :style="{ '--p': n }"></i>
          </div>
          <svg class="home-wires" viewBox="0 0 1200 700" preserveAspectRatio="none">
            <path class="home-wire home-wire--a" d="M40 620 C220 520, 340 420, 520 380 S860 300, 1160 120" />
            <path class="home-wire home-wire--b" d="M80 680 C300 600, 420 500, 640 460 S940 380, 1180 220" />
            <circle class="home-packet home-packet--a" r="4" cx="0" cy="0">
              <animateMotion dur="5.5s" repeatCount="indefinite" path="M40 620 C220 520, 340 420, 520 380 S860 300, 1160 120" />
            </circle>
            <circle class="home-packet home-packet--b" r="3.5" cx="0" cy="0">
              <animateMotion dur="7s" repeatCount="indefinite" path="M80 680 C300 600, 420 500, 640 460 S940 380, 1180 220" />
            </circle>
          </svg>
        </div>

        <div
          class="home-hero-layout"
          :style="{
            transform: `translate3d(${parallax.x}px, ${parallax.y}px, 0)`
          }"
        >
          <div class="home-hero-stack">
            <p class="home-kicker">
              <span class="home-live-dot" aria-hidden="true"></span>
              {{ t('home.signalLive') }}
            </p>
            <h1 id="home-brand-title" class="home-mega">
              <span class="home-mega-line">{{ siteName }}</span>
              <span class="home-mega-glitch" aria-hidden="true">{{ siteName }}</span>
            </h1>
            <p class="home-mega-sub">{{ siteSubtitle }}</p>
            <div class="home-signal-bars" aria-hidden="true">
              <span v-for="n in 8" :key="`bar-${n}`" :style="{ '--b': n }"></span>
            </div>
          </div>

          <aside class="home-hero-panel">
            <div class="home-panel-chrome" aria-hidden="true">
              <span></span><span></span><span></span>
            </div>
            <p class="home-panel-code">
              <span>// relay.core</span>
              <span class="home-caret"></span>
            </p>
            <p class="home-lede">{{ t('home.heroDescription') }}</p>
            <div class="home-stream" aria-hidden="true">
              <span v-for="line in streamLines" :key="line">{{ line }}</span>
            </div>
            <ul class="home-tag-rail" aria-label="capabilities">
              <li>{{ t('home.tags.subscriptionToApi') }}</li>
              <li>{{ t('home.tags.stickySession') }}</li>
              <li>{{ t('home.tags.realtimeBilling') }}</li>
            </ul>
            <div class="home-hero-actions">
              <router-link :to="isAuthenticated ? dashboardPath : '/login'" class="home-primary-action">
                <span class="home-primary-glow" aria-hidden="true"></span>
                <span class="home-primary-sheen" aria-hidden="true"></span>
                <span>{{ isAuthenticated ? t('home.goToDashboard') : t('home.getStarted') }}</span>
                <Icon name="arrowRight" size="md" class="home-primary-arrow" />
              </router-link>
              <a
                v-if="docUrl"
                :href="docUrl"
                target="_blank"
                rel="noopener noreferrer"
                class="home-secondary-action"
              >
                {{ t('home.viewDocs') }}
              </a>
              <router-link
                v-else-if="isAuthenticated"
                to="/image-workbench"
                class="home-secondary-action"
              >
                {{ t('home.openWorkbench') }}
              </router-link>
            </div>
          </aside>
        </div>

        <div class="home-ticker" aria-hidden="true">
          <div class="home-ticker-track">
            <span v-for="item in tickerLoop" :key="`a-${item}`">{{ item }}</span>
          </div>
        </div>
        <div class="home-ticker home-ticker--reverse" aria-hidden="true">
          <div class="home-ticker-track">
            <span v-for="item in tickerLoop" :key="`b-${item}`">{{ item }}</span>
          </div>
        </div>
      </section>

      <section class="home-rift" :aria-label="t('home.solutions.title')">
        <header class="home-rift-head">
          <p class="home-rift-index">01 / SIGNAL MAP</p>
          <h2>{{ t('home.solutions.title') }}</h2>
          <p>{{ t('home.solutions.subtitle') }}</p>
        </header>

        <div class="home-rift-stack">
          <article
            v-for="(feature, index) in featureCards"
            :key="feature.title"
            class="home-slab"
            :style="{ '--slab-i': index }"
          >
            <span class="home-slab-num">0{{ index + 1 }}</span>
            <div class="home-slab-body">
              <h3>{{ feature.title }}</h3>
              <p>{{ feature.desc }}</p>
            </div>
            <span class="home-slab-arrow" aria-hidden="true">↗</span>
          </article>
        </div>
      </section>

      <section class="home-constellation" :aria-label="t('home.providers.title')">
        <div class="home-constellation-copy">
          <p class="home-rift-index">02 / MODEL MESH</p>
          <h2>{{ t('home.providers.title') }}</h2>
          <p>{{ t('home.providers.description') }}</p>
        </div>

        <div class="home-orbit-field" aria-hidden="true">
          <div class="home-orbit-core">
            <span class="home-orbit-ring-pulse"></span>
            <span class="home-orbit-ring-pulse home-orbit-ring-pulse--delay"></span>
            <strong>ONE KEY</strong>
            <span>{{ t('home.providers.supported') }}</span>
          </div>
          <span
            v-for="(name, index) in providers"
            :key="name"
            class="home-orbit-chip"
            :style="{ '--i': index, '--n': providers.length }"
          >
            {{ name }}
          </span>
        </div>
      </section>
    </main>

    <footer class="home-footer">
      <div class="home-footer-frame">
        <div class="home-footer-corners" aria-hidden="true">
          <i></i><i></i><i></i><i></i>
        </div>
        <div class="home-footer-scan" aria-hidden="true"></div>

        <div class="home-footer-main">
          <div class="home-footer-brand">
            <span class="home-footer-mark">{{ siteName }}</span>
            <p class="home-footer-copy">{{ t('home.footer.tagline') }}</p>
            <p class="home-footer-meta">&copy; {{ currentYear }} {{ siteName }}. {{ t('home.footer.allRightsReserved') }}</p>
          </div>

          <div class="home-footer-links">
            <p class="home-footer-label">{{ t('home.footer.channels') }}</p>
            <div class="home-footer-link-row">
              <a
                v-if="docUrl"
                :href="docUrl"
                target="_blank"
                rel="noopener noreferrer"
                class="home-footer-link"
              >
                <span>{{ t('home.docs') }}</span>
                <Icon name="arrowRight" size="sm" />
              </a>
              <router-link :to="isAuthenticated ? dashboardPath : '/login'" class="home-footer-link">
                <span>{{ isAuthenticated ? t('home.dashboard') : t('home.login') }}</span>
                <Icon name="arrowRight" size="sm" />
              </router-link>
              <router-link
                v-if="isAuthenticated"
                to="/image-workbench"
                class="home-footer-link"
              >
                <span>{{ t('home.openWorkbench') }}</span>
                <Icon name="arrowRight" size="sm" />
              </router-link>
            </div>
          </div>

          <div class="home-footer-telemetry" aria-hidden="true">
            <div class="home-telemetry-item">
              <span class="home-telemetry-key">SYS</span>
              <span class="home-telemetry-val">NOMINAL</span>
            </div>
            <div class="home-telemetry-item">
              <span class="home-telemetry-key">LAT</span>
              <span class="home-telemetry-val">{{ latencyHint }}</span>
            </div>
            <div class="home-telemetry-item">
              <span class="home-telemetry-key">NET</span>
              <span class="home-telemetry-val home-telemetry-val--live">
                <span class="home-live-dot"></span>
                LIVE
              </span>
            </div>
          </div>
        </div>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import DOMPurify from 'dompurify'
import { useAppStore, useAuthStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import { sanitizeUrl } from '@/utils/url'

const { t } = useI18n()
const authStore = useAuthStore()
const appStore = useAppStore()

const siteName = computed(() => appStore.cachedPublicSettings?.site_name || appStore.siteName || 'Sub2API')
const siteSubtitle = computed(() => {
  const fromSettings = appStore.cachedPublicSettings?.site_subtitle?.trim()
  return fromSettings || t('home.heroSubtitle')
})
const siteLogo = computed(() =>
  sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo || '', {
    allowRelative: true,
    allowDataUrl: true
  })
)
const docUrl = computed(() => sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl || ''))
const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')
const hasHomeContent = computed(() => homeContent.value.trim().length > 0)
const isHomeContentUrl = computed(() => /^https?:\/\//i.test(homeContent.value.trim()))
const iframeHomeUrl = computed(() => (isHomeContentUrl.value ? sanitizeUrl(homeContent.value.trim()) : ''))
const sanitizedHomeContent = computed(() =>
  DOMPurify.sanitize(homeContent.value, {
    USE_PROFILES: { html: true },
    FORBID_TAGS: ['script', 'iframe', 'object', 'embed', 'base', 'form'],
    FORBID_ATTR: ['srcdoc']
  })
)
const compactHomeEnabled = computed(() => appStore.cachedPublicSettings?.compact_home_enabled === true)

const isDark = ref(document.documentElement.classList.contains('dark'))
const ready = ref(false)
const isAuthenticated = computed(() => authStore.isAuthenticated)
const dashboardPath = computed(() => (authStore.isAdmin ? '/admin/dashboard' : '/dashboard'))
const currentYear = computed(() => new Date().getFullYear())
const latencyHint = ref('12ms')
const pointer = reactive({ x: -200, y: -200 })
const parallax = reactive({ x: 0, y: 0 })
let latencyTimer: number | undefined

const providers = ['Anthropic', 'OpenAI', 'Gemini', 'Grok', 'Antigravity']
const streamLines = [
  '> route.open(gemini)',
  '> failover.probe(ok)',
  '> billing.meter(+1)',
  '> queue.ack(async)'
]
const tickerItems = computed(() => [
  siteName.value.toUpperCase(),
  'MULTI-MODEL RELAY',
  'USAGE BILLING',
  'FAILOVER READY',
  'ONE KEY'
])
const tickerLoop = computed(() => [...tickerItems.value, ...tickerItems.value])

const featureCards = computed(() => [
  {
    title: t('home.features.unifiedGateway'),
    desc: t('home.features.unifiedGatewayDesc')
  },
  {
    title: t('home.features.balanceQuota'),
    desc: t('home.features.balanceQuotaDesc')
  },
  {
    title: t('home.features.multiAccount'),
    desc: t('home.features.multiAccountDesc')
  }
])

function onPointerMove(event: PointerEvent) {
  pointer.x = event.clientX
  pointer.y = event.clientY
  const nx = event.clientX / window.innerWidth - 0.5
  const ny = event.clientY / window.innerHeight - 0.5
  parallax.x = nx * -14
  parallax.y = ny * -10
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

function initTheme() {
  const savedTheme = localStorage.getItem('theme')
  // Home 默认夜间：仅当用户明确选过浅色时才保持浅色
  isDark.value = savedTheme !== 'light'
  document.documentElement.classList.toggle('dark', isDark.value)
  if (!savedTheme) {
    localStorage.setItem('theme', 'dark')
  }
}

onMounted(() => {
  initTheme()
  authStore.checkAuth()
  if (!appStore.publicSettingsLoaded) appStore.fetchPublicSettings()
  latencyHint.value = `${8 + Math.floor(Math.random() * 18)}ms`
  latencyTimer = window.setInterval(() => {
    latencyHint.value = `${8 + Math.floor(Math.random() * 18)}ms`
  }, 2400)
  requestAnimationFrame(() => {
    ready.value = true
  })
})

onUnmounted(() => {
  if (latencyTimer) window.clearInterval(latencyTimer)
})
</script>

<style scoped>
.home-shell {
  --home-bg: #e8ecef;
  --home-bg-deep: #cfd8dd;
  --home-surface: rgba(255, 255, 255, 0.78);
  --home-ink: #0d1b21;
  --home-muted: #5a6c73;
  --home-line: rgba(13, 27, 33, 0.14);
  --home-accent: #067a6f;
  --home-signal: #d4891a;
  --home-acid: #9dffb0;
  --home-hero-ink: #0d1b21;
  --home-hero-muted: rgba(13, 27, 33, 0.7);
  --home-action-ink: #04201c;
  --home-header-bg: rgba(232, 236, 239, 0.78);
  position: relative;
  min-height: 100vh;
  overflow-x: clip;
  color: var(--home-ink);
  background:
    radial-gradient(900px 500px at 90% 0%, rgba(6, 122, 111, 0.2), transparent 55%),
    radial-gradient(700px 480px at 0% 70%, rgba(212, 137, 26, 0.16), transparent 50%),
    linear-gradient(155deg, var(--home-bg), var(--home-bg-deep));
  font-family: 'Sora', ui-sans-serif, system-ui, sans-serif;
}

:global(html.dark .home-shell) {
  --home-bg: #081116;
  --home-bg-deep: #050b0e;
  --home-surface: rgba(14, 24, 29, 0.82);
  --home-ink: #e8f1f4;
  --home-muted: #90a2a9;
  --home-line: rgba(232, 241, 244, 0.12);
  --home-accent: #2fd0bc;
  --home-signal: #efb143;
  --home-acid: #9dffb0;
  --home-hero-ink: #f2f8fa;
  --home-hero-muted: rgba(232, 241, 244, 0.72);
  --home-action-ink: #04201c;
  --home-header-bg: rgba(8, 17, 22, 0.78);
}

.home-noise,
.home-vignette,
.home-cursor-glow {
  pointer-events: none;
  position: fixed;
  inset: 0;
  z-index: 0;
}

.home-noise {
  opacity: 0.05;
  background-image: url("data:image/svg+xml,%3Csvg viewBox='0 0 160 160' xmlns='http://www.w3.org/2000/svg'%3E%3Cfilter id='n'%3E%3CfeTurbulence type='fractalNoise' baseFrequency='0.85' numOctaves='3' stitchTiles='stitch'/%3E%3C/filter%3E%3Crect width='100%25' height='100%25' filter='url(%23n)'/%3E%3C/svg%3E");
}

.home-vignette {
  background: radial-gradient(circle at center, transparent 40%, rgba(0, 0, 0, 0.18) 100%);
}

.home-cursor-glow {
  inset: auto;
  width: 280px;
  height: 280px;
  margin: -140px 0 0 -140px;
  border-radius: 50%;
  background: radial-gradient(circle, color-mix(in srgb, var(--home-accent) 22%, transparent), transparent 70%);
  opacity: 0.7;
  transition: opacity 200ms ease;
  z-index: 0;
  will-change: transform;
}

.home-header,
main,
.home-footer {
  position: relative;
  z-index: 1;
}

.home-header {
  position: sticky;
  top: 0;
  z-index: 30;
  padding: 14px clamp(18px, 4vw, 56px);
  border-bottom: 1px solid var(--home-line);
  background:
    linear-gradient(180deg, color-mix(in srgb, var(--home-accent) 10%, transparent), transparent 72%),
    var(--home-header-bg);
  backdrop-filter: blur(16px) saturate(1.15);
}

.home-header-beam {
  position: absolute;
  left: 8%;
  right: 8%;
  bottom: -1px;
  height: 2px;
  background: linear-gradient(90deg, transparent, var(--home-accent), var(--home-signal), var(--home-accent), transparent);
  background-size: 220% 100%;
  animation: homeHeaderBeam 4.5s linear infinite;
}

.home-nav {
  display: grid;
  grid-template-columns: 1fr auto 1fr;
  align-items: center;
  gap: 16px;
  max-width: 1280px;
  margin: 0 auto;
}

.home-brand,
.home-actions,
.home-session-link,
.home-hero-actions,
.home-primary-action,
.home-secondary-action {
  display: flex;
  align-items: center;
}

.home-brand {
  min-width: 0;
  gap: 12px;
  justify-self: start;
  color: var(--home-ink);
  text-decoration: none;
}

.home-logo {
  position: relative;
  display: grid;
  width: 44px;
  height: 44px;
  place-items: center;
  border: 1px solid var(--home-line);
  border-radius: 12px;
  background: var(--home-surface);
  transition: border-color 220ms ease, transform 220ms ease, box-shadow 220ms ease;
}

.home-logo-ring {
  position: absolute;
  inset: -5px;
  border: 1px dashed color-mix(in srgb, var(--home-accent) 55%, transparent);
  border-radius: 16px;
  opacity: 0;
  transition: opacity 220ms ease;
}

.home-logo img {
  width: 70%;
  height: 70%;
  object-fit: contain;
  transition: transform 280ms ease;
}

.home-brand:hover .home-logo,
.home-brand:focus-visible .home-logo {
  border-color: var(--home-accent);
  transform: translateY(-1px);
  box-shadow: 0 0 0 4px color-mix(in srgb, var(--home-accent) 16%, transparent);
}

.home-brand:hover .home-logo-ring,
.home-brand:focus-visible .home-logo-ring {
  opacity: 1;
  animation: homeLogoSpin 6s linear infinite;
}

.home-brand:hover .home-logo img {
  transform: scale(1.08);
}

.home-brand-meta {
  display: grid;
  gap: 2px;
}

.home-brand-text {
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 15px;
  font-weight: 700;
  letter-spacing: 0.05em;
  text-transform: none;
}

.home-brand-sub {
  color: var(--home-muted);
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.18em;
}

.home-status-chip {
  display: inline-flex;
  align-items: center;
  justify-self: center;
  gap: 8px;
  padding: 8px 14px;
  border: 1px solid color-mix(in srgb, var(--home-accent) 35%, var(--home-line));
  border-radius: 999px;
  color: var(--home-accent);
  background: color-mix(in srgb, var(--home-accent) 10%, var(--home-surface));
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  transition: transform 220ms ease, border-color 220ms ease;
}

.home-status-chip:hover {
  transform: translateY(-1px) scale(1.02);
  border-color: var(--home-accent);
}

.home-actions {
  gap: 8px;
  justify-self: end;
}

.home-icon-button {
  position: relative;
  display: grid;
  width: 42px;
  height: 42px;
  place-items: center;
  overflow: hidden;
  border: 1px solid var(--home-line);
  border-radius: 12px;
  color: var(--home-ink);
  background: var(--home-surface);
  transition: border-color 200ms ease, transform 200ms ease, color 200ms ease, box-shadow 200ms ease;
}

.home-icon-sheen {
  position: absolute;
  inset: 0;
  background: linear-gradient(120deg, transparent 30%, color-mix(in srgb, var(--home-accent) 35%, transparent) 50%, transparent 70%);
  transform: translateX(-120%);
  transition: transform 420ms ease;
}

.home-icon-button:hover,
.home-icon-button:focus-visible {
  border-color: var(--home-accent);
  color: var(--home-accent);
  transform: translateY(-2px);
  box-shadow: 0 8px 20px color-mix(in srgb, var(--home-accent) 18%, transparent);
}

.home-icon-button:hover .home-icon-sheen,
.home-icon-button:focus-visible .home-icon-sheen {
  transform: translateX(120%);
}

.home-session-link {
  position: relative;
  min-height: 42px;
  overflow: hidden;
  gap: 8px;
  padding: 0 16px;
  border-radius: 12px;
  color: var(--home-action-ink);
  background: var(--home-accent);
  font-size: 13px;
  font-weight: 700;
  text-decoration: none;
  transition: transform 200ms ease, box-shadow 200ms ease;
}

.home-session-glow {
  position: absolute;
  inset: 0;
  background: linear-gradient(110deg, transparent 20%, rgba(255, 255, 255, 0.35) 45%, transparent 70%);
  transform: translateX(-130%);
  transition: transform 480ms ease;
}

.home-session-arrow {
  transition: transform 200ms ease;
}

.home-session-link:hover,
.home-session-link:focus-visible {
  transform: translateY(-2px);
  box-shadow: 0 10px 24px color-mix(in srgb, var(--home-accent) 28%, transparent);
}

.home-session-link:hover .home-session-glow,
.home-session-link:focus-visible .home-session-glow {
  transform: translateX(130%);
}

.home-session-link:hover .home-session-arrow {
  transform: translateX(3px);
}

.home-hero {
  position: relative;
  min-height: auto;
  display: grid;
  align-content: start;
  overflow: hidden;
  padding: clamp(28px, 5vh, 56px) 0 0;
}

.home-hero-bg {
  position: absolute;
  inset: 0;
  overflow: hidden;
}

.home-grid {
  position: absolute;
  inset: -12%;
  background-image:
    linear-gradient(var(--home-line) 1px, transparent 1px),
    linear-gradient(90deg, var(--home-line) 1px, transparent 1px);
  background-size: 64px 64px;
  transform: rotate(-8deg) scale(1.2);
  mask-image: radial-gradient(circle at 55% 40%, black, transparent 70%);
  animation: homeGridDrift 16s linear infinite;
}

.home-scan {
  position: absolute;
  left: 0;
  right: 0;
  height: 22%;
  background: linear-gradient(180deg, transparent, color-mix(in srgb, var(--home-accent) 20%, transparent), transparent);
  animation: homeScan 5.2s ease-in-out infinite;
}

.home-scan--slow {
  height: 12%;
  opacity: 0.55;
  animation-duration: 9s;
  animation-delay: 1.4s;
  background: linear-gradient(180deg, transparent, color-mix(in srgb, var(--home-signal) 18%, transparent), transparent);
}

.home-rain {
  position: absolute;
  inset: 0;
  overflow: hidden;
  opacity: 0.35;
}

.home-rain span {
  position: absolute;
  top: -20%;
  left: calc(var(--r) * 7%);
  width: 1px;
  height: 18%;
  background: linear-gradient(180deg, transparent, var(--home-accent), transparent);
  animation: homeRainFall calc(3s + var(--r) * 0.25s) linear infinite;
  animation-delay: calc(var(--r) * -0.35s);
}

.home-particles {
  position: absolute;
  inset: 0;
  overflow: hidden;
}

.home-particles i {
  position: absolute;
  left: calc((var(--p) * 53) % 100 * 1%);
  bottom: -8%;
  width: 4px;
  height: 4px;
  border-radius: 50%;
  background: var(--home-accent);
  opacity: 0.55;
  animation: homeParticleRise calc(7s + var(--p) * 0.35s) linear infinite;
  animation-delay: calc(var(--p) * -0.45s);
  box-shadow: 0 0 10px color-mix(in srgb, var(--home-accent) 60%, transparent);
}

.home-wires {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  opacity: 0.55;
}

.home-wire {
  fill: none;
  stroke: color-mix(in srgb, var(--home-accent) 55%, transparent);
  stroke-width: 1.2;
  stroke-dasharray: 8 12;
  animation: homeWireDash 18s linear infinite;
}

.home-wire--b {
  stroke: color-mix(in srgb, var(--home-signal) 55%, transparent);
  animation-duration: 24s;
  animation-direction: reverse;
}

.home-packet {
  fill: var(--home-accent);
  filter: drop-shadow(0 0 6px color-mix(in srgb, var(--home-accent) 70%, transparent));
}

.home-packet--b {
  fill: var(--home-signal);
}

.home-slash {
  position: absolute;
  top: -20%;
  right: -8%;
  width: 46vw;
  height: 140%;
  background: linear-gradient(180deg, color-mix(in srgb, var(--home-accent) 18%, transparent), transparent 70%);
  clip-path: polygon(35% 0, 100% 0, 65% 100%, 0 100%);
  animation: homeSlashPulse 6s ease-in-out infinite;
}

.home-orb {
  position: absolute;
  border-radius: 50%;
  filter: blur(2px);
}

.home-orb--a {
  top: 12%;
  right: 14%;
  width: 220px;
  height: 220px;
  background: radial-gradient(circle, color-mix(in srgb, var(--home-accent) 45%, transparent), transparent 70%);
  animation: homeFlare 7s ease-in-out infinite;
}

.home-orb--b {
  bottom: 18%;
  left: 8%;
  width: 160px;
  height: 160px;
  background: radial-gradient(circle, color-mix(in srgb, var(--home-signal) 40%, transparent), transparent 70%);
  animation: homeFlare 8.5s ease-in-out infinite reverse;
}

.home-hero-layout {
  position: relative;
  z-index: 2;
  display: grid;
  grid-template-columns: minmax(0, 1.05fr) minmax(300px, 1fr);
  gap: clamp(22px, 3.2vw, 44px);
  align-items: start;
  max-width: 1280px;
  margin: 0 auto;
  /* 与顶栏 home-nav 同一水平边界，便于右侧与登录按钮右缘对齐 */
  padding: 0 clamp(18px, 4vw, 56px);
  transition: transform 180ms ease-out;
  will-change: transform;
}

.home-hero-stack {
  margin-left: clamp(-20px, -2.4vw, -40px);
}

.home-hero-stack,
.home-hero-panel,
.home-rift-head,
.home-slab,
.home-constellation-copy {
  opacity: 0;
  transform: translateY(28px);
  transition: opacity 700ms ease, transform 700ms ease;
}

.home-shell.is-ready .home-hero-stack,
.home-shell.is-ready .home-hero-panel,
.home-shell.is-ready .home-rift-head,
.home-shell.is-ready .home-slab,
.home-shell.is-ready .home-constellation-copy {
  opacity: 1;
  transform: translateY(0);
}

.home-shell.is-ready .home-hero-panel { transition-delay: 120ms; }
.home-shell.is-ready .home-rift-head { transition-delay: 80ms; }
.home-shell.is-ready .home-slab { transition-delay: calc(140ms + var(--slab-i, 0) * 90ms); }

.home-kicker {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  margin: 0 0 18px;
  color: var(--home-accent);
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 12px;
  font-weight: 600;
  letter-spacing: 0.18em;
  text-transform: uppercase;
}

.home-live-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--home-accent);
  box-shadow: 0 0 0 0 color-mix(in srgb, var(--home-accent) 55%, transparent);
  animation: homeLive 1.8s ease-out infinite;
}

.home-mega {
  position: relative;
  margin: 0;
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: clamp(52px, 9.2vw, 112px);
  font-weight: 700;
  line-height: 0.9;
  letter-spacing: -0.03em;
}

.home-mega-line {
  display: inline-block;
  background: linear-gradient(105deg, var(--home-ink) 20%, var(--home-accent) 55%, var(--home-signal) 85%);
  background-size: 180% 100%;
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
  animation: homeTextShift 8s ease-in-out infinite;
  overflow-wrap: anywhere;
}

.home-mega-glitch {
  position: absolute;
  inset: 0;
  display: inline-block;
  color: color-mix(in srgb, var(--home-accent) 55%, transparent);
  opacity: 0;
  mix-blend-mode: screen;
  animation: homeGlitch 5.5s steps(2, end) infinite;
  pointer-events: none;
}

.home-mega-sub {
  margin: 20px 0 0;
  max-width: 16em;
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: clamp(22px, 3vw, 34px);
  font-weight: 600;
  line-height: 1.25;
  letter-spacing: -0.01em;
}

.home-signal-bars {
  display: flex;
  align-items: flex-end;
  gap: 6px;
  height: 42px;
  margin-top: 26px;
}

.home-signal-bars span {
  width: 7px;
  height: 20%;
  border-radius: 999px;
  background: linear-gradient(180deg, var(--home-accent), color-mix(in srgb, var(--home-accent) 20%, transparent));
  animation: homeBarPulse calc(0.8s + var(--b) * 0.12s) ease-in-out infinite;
  animation-delay: calc(var(--b) * 0.08s);
}

.home-hero-panel {
  position: relative;
  width: 100%;
  max-width: 520px;
  min-width: 0;
  justify-self: end;
  margin-top: clamp(18px, 3vh, 36px);
  margin-right: clamp(-28px, -4vw, -64px);
  padding: 28px 26px 24px;
  border: 1px solid var(--home-line);
  border-radius: 22px;
  background: color-mix(in srgb, var(--home-surface) 88%, transparent);
  backdrop-filter: blur(10px);
  transform: translateX(clamp(36px, 4.5vw, 72px)) rotate(-1deg);
  transform-origin: right top;
  transition: transform 280ms ease, border-color 280ms ease, box-shadow 280ms ease;
  overflow: hidden;
}

.home-hero-panel:hover {
  transform: translateX(clamp(36px, 4.5vw, 72px)) rotate(0deg) translateY(-4px);
  border-color: var(--home-accent);
  box-shadow: 0 24px 50px color-mix(in srgb, var(--home-ink) 12%, transparent);
}

.home-hero-panel::before {
  content: '';
  position: absolute;
  inset: 0;
  background: linear-gradient(120deg, transparent 40%, color-mix(in srgb, var(--home-accent) 14%, transparent) 50%, transparent 60%);
  transform: translateX(-120%);
  animation: homePanelSweep 4.8s ease-in-out infinite;
  pointer-events: none;
}

.home-panel-chrome {
  display: flex;
  gap: 6px;
  margin-bottom: 16px;
}

.home-panel-chrome span {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: color-mix(in srgb, var(--home-muted) 45%, transparent);
}

.home-panel-chrome span:first-child { background: #ff6b6b; }
.home-panel-chrome span:nth-child(2) { background: var(--home-signal); }
.home-panel-chrome span:nth-child(3) { background: var(--home-accent); }

.home-panel-code {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  margin: 0 0 10px;
  color: var(--home-accent);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  letter-spacing: 0.04em;
}

.home-caret {
  width: 7px;
  height: 1em;
  background: var(--home-accent);
  animation: homeCaret 1s steps(1, end) infinite;
}

.home-lede {
  margin: 0;
  color: var(--home-hero-muted);
  font-size: 15px;
  line-height: 1.7;
}

.home-stream {
  display: grid;
  gap: 4px;
  margin-top: 14px;
  padding: 10px 12px;
  border: 1px solid var(--home-line);
  border-radius: 12px;
  background: color-mix(in srgb, var(--home-ink) 4%, transparent);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 11px;
  color: color-mix(in srgb, var(--home-accent) 85%, var(--home-ink));
  overflow: hidden;
}

.home-stream span {
  opacity: 0;
  transform: translateY(8px);
  animation: homeStreamLine 4.8s ease-in-out infinite;
}

.home-stream span:nth-child(1) { animation-delay: 0s; }
.home-stream span:nth-child(2) { animation-delay: 0.35s; }
.home-stream span:nth-child(3) { animation-delay: 0.7s; }
.home-stream span:nth-child(4) { animation-delay: 1.05s; }

.home-tag-rail {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 18px 0 0;
  padding: 0;
  list-style: none;
}

.home-tag-rail li {
  padding: 7px 11px;
  border: 1px solid var(--home-line);
  border-radius: 999px;
  color: var(--home-muted);
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  transition: transform 180ms ease, border-color 180ms ease, color 180ms ease;
}

.home-tag-rail li:hover {
  transform: translateY(-2px);
  border-color: var(--home-accent);
  color: var(--home-accent);
}

.home-hero-actions {
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 22px;
}

.home-primary-action,
.home-secondary-action {
  position: relative;
  min-height: 48px;
  gap: 10px;
  padding: 0 18px;
  border-radius: 12px;
  font-size: 14px;
  font-weight: 700;
  text-decoration: none;
  transition: transform 200ms ease, box-shadow 200ms ease, border-color 200ms ease, filter 200ms ease;
}

.home-primary-action {
  overflow: hidden;
  color: var(--home-action-ink);
  background: var(--home-accent);
  isolation: isolate;
}

.home-primary-glow {
  position: absolute;
  inset: -40%;
  background: radial-gradient(circle, rgba(255, 255, 255, 0.35), transparent 55%);
  opacity: 0;
  transform: scale(0.6);
  transition: opacity 240ms ease, transform 240ms ease;
  z-index: -1;
}

.home-primary-sheen {
  position: absolute;
  inset: 0;
  background: linear-gradient(115deg, transparent 25%, rgba(255, 255, 255, 0.42) 48%, transparent 70%);
  transform: translateX(-130%);
  transition: transform 480ms ease;
  z-index: -1;
}

.home-primary-arrow {
  transition: transform 200ms ease;
}

.home-secondary-action {
  border: 1px solid var(--home-line);
  color: var(--home-hero-ink);
  background: color-mix(in srgb, var(--home-bg) 40%, transparent);
}

.home-primary-action:hover,
.home-primary-action:focus-visible {
  transform: translateY(-3px) scale(1.03);
  filter: brightness(1.08);
  box-shadow:
    0 0 0 4px color-mix(in srgb, var(--home-accent) 22%, transparent),
    0 14px 28px color-mix(in srgb, var(--home-accent) 35%, transparent);
}

.home-primary-action:hover .home-primary-glow,
.home-primary-action:focus-visible .home-primary-glow {
  opacity: 1;
  transform: scale(1);
}

.home-primary-action:hover .home-primary-sheen,
.home-primary-action:focus-visible .home-primary-sheen {
  transform: translateX(130%);
}

.home-primary-action:hover .home-primary-arrow,
.home-primary-action:focus-visible .home-primary-arrow {
  transform: translateX(4px);
}

.home-primary-action:active {
  transform: translateY(-1px) scale(0.99);
}

.home-secondary-action:hover,
.home-secondary-action:focus-visible {
  transform: translateY(-2px);
  border-color: var(--home-accent);
  color: var(--home-accent);
  box-shadow: 0 10px 22px color-mix(in srgb, var(--home-ink) 8%, transparent);
}

.home-ticker {
  position: relative;
  z-index: 2;
  margin-top: clamp(20px, 3.5vh, 36px);
  overflow: hidden;
  border-block: 1px solid var(--home-line);
  background: color-mix(in srgb, var(--home-ink) 4%, transparent);
}

.home-ticker--reverse {
  margin-top: 0;
  border-top: 0;
  opacity: 0.72;
}

.home-ticker--reverse .home-ticker-track {
  animation-direction: reverse;
  animation-duration: 28s;
}

.home-ticker-track {
  display: flex;
  width: max-content;
  gap: 28px;
  padding: 10px 0;
  animation: homeMarquee 24s linear infinite;
}

.home-ticker-track span {
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 13px;
  font-weight: 700;
  letter-spacing: 0.22em;
  text-transform: uppercase;
  color: var(--home-muted);
}

.home-ticker-track span::after {
  content: '◆';
  margin-left: 28px;
  color: var(--home-accent);
  animation: homeDiamondPulse 1.6s ease-in-out infinite;
}

.home-rift {
  max-width: 1280px;
  margin: 0 auto;
  padding: clamp(28px, 4.5vh, 48px) clamp(18px, 4vw, 40px) clamp(24px, 4vh, 40px);
}

.home-rift-head {
  max-width: 620px;
  margin-bottom: 22px;
}

.home-rift-index {
  margin: 0 0 10px;
  color: var(--home-accent);
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.18em;
}

.home-rift-head h2,
.home-constellation-copy h2 {
  margin: 0;
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: clamp(28px, 4vw, 42px);
  font-weight: 700;
  letter-spacing: -0.02em;
}

.home-rift-head p:last-child,
.home-constellation-copy p:last-child {
  margin: 12px 0 0;
  color: var(--home-muted);
  font-size: 15px;
  line-height: 1.65;
}

.home-rift-stack {
  display: grid;
  gap: 16px;
}

.home-slab {
  position: relative;
  display: grid;
  grid-template-columns: auto 1fr auto;
  gap: 22px;
  align-items: center;
  padding: 26px 24px;
  border: 1px solid var(--home-line);
  border-radius: 18px;
  background: var(--home-surface);
  transform: translateX(calc(var(--slab-i) * 28px)) rotate(calc((var(--slab-i) - 1) * 0.8deg));
  transition: transform 260ms ease, border-color 260ms ease, box-shadow 260ms ease, background 260ms ease;
  overflow: hidden;
}

.home-slab::after {
  content: '';
  position: absolute;
  inset: auto 0 0;
  height: 2px;
  background: linear-gradient(90deg, transparent, var(--home-accent), var(--home-signal), transparent);
  background-size: 200% 100%;
  transform: scaleX(0);
  transform-origin: left;
  transition: transform 280ms ease;
  animation: homeHeaderBeam 3s linear infinite;
}

.home-slab:hover {
  transform: translateX(calc(var(--slab-i) * 28px + 10px)) rotate(0deg) scale(1.01);
  border-color: var(--home-accent);
  box-shadow: 0 18px 40px color-mix(in srgb, var(--home-ink) 10%, transparent);
  background: color-mix(in srgb, var(--home-accent) 8%, var(--home-surface));
}

.home-slab:hover::after {
  transform: scaleX(1);
}

.home-slab-num {
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 28px;
  font-weight: 700;
  color: var(--home-accent);
}

.home-slab-body h3 {
  margin: 0;
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 22px;
  font-weight: 700;
}

.home-slab-body p {
  margin: 8px 0 0;
  color: var(--home-muted);
  font-size: 14px;
  line-height: 1.65;
}

.home-slab-arrow {
  color: var(--home-signal);
  font-size: 24px;
  transition: transform 220ms ease;
}

.home-slab:hover .home-slab-arrow {
  transform: translate(4px, -4px);
}

.home-constellation {
  display: grid;
  grid-template-columns: 0.85fr 1.15fr;
  gap: 28px;
  align-items: center;
  max-width: 1280px;
  margin: 0 auto 16px;
  padding: 0 clamp(18px, 4vw, 40px) clamp(28px, 5vh, 48px);
}

.home-orbit-field {
  position: relative;
  min-height: 360px;
  border: 1px solid var(--home-line);
  border-radius: 28px;
  background:
    radial-gradient(circle at center, color-mix(in srgb, var(--home-accent) 14%, transparent), transparent 55%),
    var(--home-surface);
  overflow: hidden;
}

.home-orbit-core {
  position: absolute;
  top: 50%;
  left: 50%;
  z-index: 2;
  display: grid;
  place-items: center;
  width: 132px;
  height: 132px;
  border: 1px solid var(--home-accent);
  border-radius: 50%;
  background: color-mix(in srgb, var(--home-bg) 70%, transparent);
  transform: translate(-50%, -50%);
  text-align: center;
  box-shadow: 0 0 0 10px color-mix(in srgb, var(--home-accent) 10%, transparent);
  animation: homeCorePulse 3.6s ease-in-out infinite;
}

.home-orbit-ring-pulse {
  position: absolute;
  inset: -18px;
  border: 1px solid color-mix(in srgb, var(--home-accent) 45%, transparent);
  border-radius: 50%;
  animation: homeRingExpand 2.8s ease-out infinite;
}

.home-orbit-ring-pulse--delay {
  animation-delay: 1.4s;
}

.home-orbit-core strong {
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 16px;
  letter-spacing: 0.08em;
}

.home-orbit-core span {
  color: var(--home-muted);
  font-size: 11px;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}

.home-orbit-chip {
  position: absolute;
  top: 50%;
  left: 50%;
  padding: 10px 14px;
  border: 1px solid var(--home-line);
  border-radius: 999px;
  background: color-mix(in srgb, var(--home-bg) 55%, transparent);
  color: var(--home-ink);
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  transform:
    translate(-50%, -50%)
    rotate(calc(var(--i) * (360deg / var(--n))))
    translateY(-128px)
    rotate(calc(var(--i) * (-360deg / var(--n))));
  animation: homeOrbitChip 16s linear infinite;
  animation-delay: calc(var(--i) * -1.2s);
  transition: border-color 200ms ease, color 200ms ease, background 200ms ease;
}

.home-orbit-chip:hover {
  border-color: var(--home-accent);
  color: var(--home-accent);
  background: color-mix(in srgb, var(--home-accent) 12%, transparent);
  z-index: 3;
}

.home-footer {
  max-width: 1280px;
  margin: 0 auto;
  padding: 0 clamp(18px, 4vw, 40px) 40px;
}

.home-footer-frame {
  position: relative;
  overflow: hidden;
  padding: 28px 24px;
  border: 1px solid var(--home-line);
  border-radius: 18px;
  background:
    linear-gradient(145deg, color-mix(in srgb, var(--home-accent) 8%, transparent), transparent 40%),
    var(--home-surface);
  transition: border-color 240ms ease, transform 240ms ease, box-shadow 240ms ease;
}

.home-footer-frame:hover {
  border-color: color-mix(in srgb, var(--home-accent) 45%, var(--home-line));
  transform: translateY(-2px);
  box-shadow: 0 16px 40px color-mix(in srgb, var(--home-ink) 8%, transparent);
}

.home-footer-corners i {
  position: absolute;
  width: 14px;
  height: 14px;
  border-color: var(--home-accent);
  border-style: solid;
  opacity: 0.7;
  transition: opacity 220ms ease, transform 220ms ease;
}

.home-footer-corners i:nth-child(1) { top: 10px; left: 10px; border-width: 2px 0 0 2px; }
.home-footer-corners i:nth-child(2) { top: 10px; right: 10px; border-width: 2px 2px 0 0; }
.home-footer-corners i:nth-child(3) { bottom: 10px; left: 10px; border-width: 0 0 2px 2px; }
.home-footer-corners i:nth-child(4) { bottom: 10px; right: 10px; border-width: 0 2px 2px 0; }

.home-footer-frame:hover .home-footer-corners i {
  opacity: 1;
  transform: scale(1.15);
}

.home-footer-scan {
  position: absolute;
  left: 0;
  right: 0;
  height: 28%;
  background: linear-gradient(180deg, transparent, color-mix(in srgb, var(--home-accent) 12%, transparent), transparent);
  animation: homeFooterScan 7s ease-in-out infinite;
  pointer-events: none;
}

.home-footer-main {
  position: relative;
  z-index: 1;
  display: grid;
  grid-template-columns: 1.4fr 1fr auto;
  gap: 28px;
}

.home-footer-mark {
  display: inline-block;
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 20px;
  font-weight: 700;
  color: var(--home-ink);
  transition: color 220ms ease, letter-spacing 220ms ease;
}

.home-footer-frame:hover .home-footer-mark {
  color: var(--home-accent);
  letter-spacing: 0.08em;
}

.home-footer-copy {
  margin: 10px 0 0;
  max-width: 36em;
  color: var(--home-muted);
  font-size: 13px;
  line-height: 1.6;
}

.home-footer-meta {
  margin: 14px 0 0;
  font-size: 12px;
  opacity: 0.8;
}

.home-footer-label {
  margin: 0 0 12px;
  font-family: 'Oxanium', 'Sora', sans-serif;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  color: var(--home-accent);
}

.home-footer-link-row {
  display: grid;
  gap: 8px;
}

.home-footer-link {
  display: inline-flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-width: 180px;
  padding: 10px 12px;
  border: 1px solid var(--home-line);
  border-radius: 10px;
  color: var(--home-ink);
  background: color-mix(in srgb, var(--home-bg) 50%, transparent);
  font-size: 13px;
  font-weight: 600;
  text-decoration: none;
  transition: border-color 200ms ease, transform 200ms ease, background 200ms ease, color 200ms ease;
}

.home-footer-link:hover,
.home-footer-link:focus-visible {
  border-color: var(--home-accent);
  color: var(--home-accent);
  background: color-mix(in srgb, var(--home-accent) 10%, transparent);
  transform: translateX(4px);
}

.home-footer-telemetry {
  display: grid;
  gap: 8px;
  min-width: 150px;
  padding: 12px;
  border: 1px solid var(--home-line);
  border-radius: 12px;
  background: color-mix(in srgb, var(--home-ink) 3%, transparent);
  font-family: 'Oxanium', 'Sora', sans-serif;
}

.home-telemetry-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  font-size: 11px;
  letter-spacing: 0.08em;
}

.home-telemetry-key { color: var(--home-muted); }
.home-telemetry-val { color: var(--home-ink); font-weight: 700; }
.home-telemetry-val--live {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--home-accent);
}

.custom-home-content { min-height: 100vh; }

@keyframes homeGridDrift {
  from { transform: rotate(-8deg) scale(1.2) translate3d(0, 0, 0); }
  to { transform: rotate(-8deg) scale(1.2) translate3d(-64px, -64px, 0); }
}

@keyframes homeScan {
  0%, 100% { top: -22%; opacity: 0.15; }
  40% { opacity: 0.7; }
  100% { top: 100%; opacity: 0.1; }
}

@keyframes homeFlare {
  0%, 100% { opacity: 0.55; transform: scale(1); }
  50% { opacity: 0.95; transform: scale(1.1); }
}

@keyframes homeLive {
  0% { box-shadow: 0 0 0 0 color-mix(in srgb, var(--home-accent) 55%, transparent); }
  70% { box-shadow: 0 0 0 12px transparent; }
  100% { box-shadow: 0 0 0 0 transparent; }
}

@keyframes homeMarquee {
  from { transform: translateX(0); }
  to { transform: translateX(-50%); }
}

@keyframes homeHeaderBeam {
  from { background-position: 0% 0; }
  to { background-position: 220% 0; }
}

@keyframes homeLogoSpin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

@keyframes homeFooterScan {
  0%, 100% { top: -30%; opacity: 0.2; }
  45% { opacity: 0.65; }
  100% { top: 110%; opacity: 0.15; }
}

@keyframes homeSlashPulse {
  0%, 100% { opacity: 0.45; }
  50% { opacity: 0.85; }
}

@keyframes homeTextShift {
  0%, 100% { background-position: 0% 50%; }
  50% { background-position: 100% 50%; }
}

@keyframes homeCorePulse {
  0%, 100% { box-shadow: 0 0 0 10px color-mix(in srgb, var(--home-accent) 10%, transparent); }
  50% { box-shadow: 0 0 0 18px color-mix(in srgb, var(--home-accent) 4%, transparent); }
}

@keyframes homeOrbitChip {
  from {
    transform:
      translate(-50%, -50%)
      rotate(calc(var(--i) * (360deg / var(--n))))
      translateY(-128px)
      rotate(calc(var(--i) * (-360deg / var(--n))));
  }
  to {
    transform:
      translate(-50%, -50%)
      rotate(calc(var(--i) * (360deg / var(--n)) + 360deg))
      translateY(-128px)
      rotate(calc(var(--i) * (-360deg / var(--n)) - 360deg));
  }
}

@keyframes homeRainFall {
  from { transform: translateY(0); opacity: 0; }
  15% { opacity: 0.9; }
  to { transform: translateY(140vh); opacity: 0; }
}

@keyframes homeParticleRise {
  from { transform: translate3d(0, 0, 0) scale(0.6); opacity: 0; }
  20% { opacity: 0.8; }
  to { transform: translate3d(20px, -110vh, 0) scale(1.2); opacity: 0; }
}

@keyframes homeWireDash {
  from { stroke-dashoffset: 0; }
  to { stroke-dashoffset: -240; }
}

@keyframes homeGlitch {
  0%, 90%, 100% { opacity: 0; transform: translate(0, 0); }
  91% { opacity: 0.55; transform: translate(3px, -1px); }
  93% { opacity: 0.35; transform: translate(-4px, 1px); }
  95% { opacity: 0.5; transform: translate(2px, 0); }
}

@keyframes homeBarPulse {
  0%, 100% { height: 22%; }
  50% { height: 92%; }
}

@keyframes homeCaret {
  0%, 49% { opacity: 1; }
  50%, 100% { opacity: 0; }
}

@keyframes homePanelSweep {
  0%, 55%, 100% { transform: translateX(-120%); }
  70% { transform: translateX(120%); }
}

@keyframes homeStreamLine {
  0%, 8% { opacity: 0; transform: translateY(8px); }
  18%, 70% { opacity: 1; transform: translateY(0); }
  82%, 100% { opacity: 0.15; transform: translateY(-4px); }
}

@keyframes homeDiamondPulse {
  0%, 100% { opacity: 0.45; }
  50% { opacity: 1; }
}

@keyframes homeRingExpand {
  0% { transform: scale(0.85); opacity: 0.7; }
  100% { transform: scale(1.45); opacity: 0; }
}

@media (max-width: 980px) {
  .home-nav { grid-template-columns: 1fr auto; }
  .home-status-chip { display: none; }
  .home-actions > :deep(.locale-switcher) { display: none; }
  .home-hero-layout,
  .home-constellation,
  .home-footer-main {
    grid-template-columns: 1fr;
  }
  .home-mega-sub { max-width: none; }
  .home-hero-stack {
    margin-left: 0;
  }
  .home-hero-panel {
    max-width: none;
    justify-self: stretch;
    margin-top: 12px;
    margin-right: 0;
    transform: none;
  }
  .home-slab { transform: none; }
  .home-slab:hover { transform: translateY(-3px); }
  .home-orbit-field { min-height: 320px; }
  .home-cursor-glow,
  .home-rain,
  .home-wires {
    display: none;
  }
}

@media (max-width: 560px) {
  .home-header { padding: 12px 14px; }
  .home-brand-meta { display: none; }
  .home-mega { font-size: 42px; }
  .home-footer-link { min-width: 0; width: 100%; }
  .home-orbit-chip {
    transform:
      translate(-50%, -50%)
      rotate(calc(var(--i) * (360deg / var(--n))))
      translateY(-100px)
      rotate(calc(var(--i) * (-360deg / var(--n))));
  }
}

@media (prefers-reduced-motion: reduce) {
  .home-shell *,
  .home-shell *::before,
  .home-shell *::after {
    animation: none !important;
    transition: none !important;
  }

  .home-hero-stack,
  .home-hero-panel,
  .home-rift-head,
  .home-slab,
  .home-constellation-copy {
    opacity: 1;
    transform: none;
  }
}
</style>
