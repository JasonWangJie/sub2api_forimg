<template>
  <header class="app-header sticky top-0 z-30">
    <div class="app-header__beam" aria-hidden="true"></div>
    <div class="app-header__inner flex h-16 items-center justify-between gap-2 px-2 sm:px-4 md:px-6">
      <!-- Left: Mobile Menu Toggle + Page Title -->
      <div class="flex min-w-0 shrink-0 items-center gap-2 sm:gap-4">
        <button
          @click="toggleMobileSidebar"
          class="app-header__icon-btn btn-ghost btn-icon lg:hidden"
          :aria-label="t('common.toggleMenu')"
        >
          <Icon name="menu" size="md" />
        </button>

        <div class="app-header__title-block hidden min-w-0 lg:block">
          <h1 class="app-header__title truncate">
            {{ pageTitle }}
          </h1>
          <p v-if="pageDescription" class="app-header__desc truncate">
            {{ pageDescription }}
          </p>
        </div>
      </div>

      <!-- Right: Infinite Canvas + Announcements + Docs + Language + Subscriptions + Balance + User Dropdown -->
      <div class="app-header__actions flex min-w-0 items-center gap-1 sm:gap-3">
        <!-- Infinite Canvas -->
        <a
          v-if="infiniteCanvasUrl"
          :href="infiniteCanvasUrl"
          target="_blank"
          rel="noopener noreferrer"
          class="app-header__chip app-header__canvas"
        >
          <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
            <path stroke-linecap="round" stroke-linejoin="round" d="M3.75 3.75h6.5v6.5h-6.5zM13.75 3.75h6.5v6.5h-6.5zM3.75 13.75h6.5v6.5h-6.5zM13.75 13.75h6.5v6.5h-6.5z" />
          </svg>
          <span class="hidden sm:inline">{{ t('nav.infiniteCanvas') }}</span>
        </a>

        <!-- Announcement Bell -->
        <AnnouncementBell v-if="user" />

        <!-- Docs Link -->
        <a
          v-if="docUrl"
          :href="docUrl"
          target="_blank"
          rel="noopener noreferrer"
          class="app-header__chip"
        >
          <Icon name="book" size="sm" />
          <span class="hidden sm:inline">{{ t('nav.docs') }}</span>
        </a>

        <!-- Model Plaza Entry -->
        <router-link
          v-if="user && modelPlazaEnabled"
          :to="{ path: '/model-plaza', query: { embedded: '1' } }"
          class="hidden items-center gap-1.5 rounded-lg px-2.5 py-1.5 text-sm font-medium text-gray-600 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-white sm:flex"
        >
          <Icon name="grid" size="sm" />
          <span class="hidden sm:inline">{{ t('nav.modelPlaza') }}</span>
        </router-link>

        <!-- Language Switcher -->
        <LocaleSwitcher />

        <!-- Subscription Progress (for users with active subscriptions) -->
        <SubscriptionProgressMini v-if="user" />

        <!-- Balance Display -->
        <div
          v-if="user"
          class="app-header__balance group relative hidden items-center gap-2 sm:flex"
        >
          <span class="app-header__balance-dot" aria-hidden="true"></span>
          <svg
            class="h-4 w-4 text-sky-600 dark:text-sky-300"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            stroke-width="1.5"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              d="M2.25 18.75a60.07 60.07 0 0115.797 2.101c.727.198 1.453-.342 1.453-1.096V18.75M3.75 4.5v.75A.75.75 0 013 6h-.75m0 0v-.375c0-.621.504-1.125 1.125-1.125H20.25M2.25 6v9m18-10.5v.75c0 .414.336.75.75.75h.75m-1.5-1.5h.375c.621 0 1.125.504 1.125 1.125v9.75c0 .621-.504 1.125-1.125 1.125h-.375m1.5-1.5H21a.75.75 0 00-.75.75v.75m0 0H3.75m0 0h-.375a1.125 1.125 0 01-1.125-1.125V15m1.5 1.5v-.75A.75.75 0 003 15h-.75M15 10.5a3 3 0 11-6 0 3 3 0 016 0zm3 0h.008v.008H18V10.5zm-12 0h.008v.008H6V10.5z"
            />
          </svg>
          <span class="text-sm font-semibold tracking-tight text-sky-800 dark:text-sky-200">
            {{ formatHeaderMoney(availableBalance) }}
          </span>
          <span
            v-if="frozenBalance > 0"
            class="rounded-md bg-amber-100/90 px-1.5 py-0.5 text-[11px] font-medium text-amber-700 dark:bg-amber-900/40 dark:text-amber-200"
          >
            {{ balanceFrozenLabel }}
          </span>
          <div
            class="app-header__balance-tip pointer-events-none absolute right-0 top-full mt-2.5 hidden w-56 p-3 text-xs group-hover:block"
          >
            <div class="flex items-center justify-between">
              <span class="text-gray-500 dark:text-dark-400">{{ balanceAvailableText }}</span>
              <span class="font-medium text-gray-900 dark:text-white">{{ formatHeaderMoney(availableBalance) }}</span>
            </div>
            <div class="mt-2 flex items-center justify-between">
              <span class="text-gray-500 dark:text-dark-400">{{ balanceFrozenText }}</span>
              <span class="font-medium text-amber-700 dark:text-amber-200">{{ formatHeaderMoney(frozenBalance) }}</span>
            </div>
            <div class="mt-2 border-t border-sky-100 pt-2 dark:border-dark-700">
              <div class="flex items-center justify-between">
                <span class="text-gray-500 dark:text-dark-400">{{ balanceTotalText }}</span>
                <span class="font-semibold text-gray-900 dark:text-white">{{ formatHeaderMoney(totalBalance) }}</span>
              </div>
            </div>
          </div>
        </div>

        <!-- User Dropdown -->
        <div v-if="user" class="relative" ref="dropdownRef">
          <button
            @click="toggleDropdown"
            class="app-header__user"
            :aria-label="t('common.userMenu')"
            :aria-expanded="dropdownOpen"
          >
            <div class="app-header__avatar">
              <img
                v-if="avatarUrl"
                :src="avatarUrl"
                :alt="displayName"
                class="h-full w-full object-cover"
              >
              <span v-else>{{ userInitials }}</span>
            </div>
            <div class="hidden min-w-0 text-left md:block">
              <div class="truncate text-sm font-medium text-slate-800 dark:text-white">
                {{ displayName }}
              </div>
              <div class="text-[11px] capitalize tracking-wide text-slate-500 dark:text-dark-400">
                {{ user.role }}
              </div>
            </div>
            <Icon
              name="chevronDown"
              size="sm"
              class="hidden text-slate-400 transition-transform duration-200 md:block"
              :class="{ 'rotate-180': dropdownOpen }"
            />
          </button>

          <!-- Dropdown Menu -->
          <transition name="dropdown">
            <div v-if="dropdownOpen" class="dropdown app-header__dropdown right-0 mt-2 w-56">
              <!-- User Info -->
              <div class="border-b border-sky-100/80 px-4 py-3 dark:border-dark-700">
                <div class="text-sm font-medium text-gray-900 dark:text-white">
                  {{ displayName }}
                </div>
                <div class="text-xs text-gray-500 dark:text-dark-400">{{ user.email }}</div>
              </div>

              <!-- Balance (mobile only) -->
              <div class="border-b border-sky-100/80 px-4 py-2 dark:border-dark-700 sm:hidden">
                <div class="text-xs text-gray-500 dark:text-dark-400">
                  {{ t('common.balance') }}
                </div>
                <div class="text-sm font-semibold text-sky-600 dark:text-sky-400">
                  {{ formatHeaderMoney(availableBalance) }}
                </div>
                <div v-if="frozenBalance > 0" class="mt-1 text-xs text-amber-600 dark:text-amber-300">
                  {{ balanceFrozenText }} {{ formatHeaderMoney(frozenBalance) }}
                </div>
              </div>

              <div class="py-1">
                <router-link to="/profile" @click="closeDropdown" class="dropdown-item">
                  <Icon name="user" size="sm" />
                  {{ t('nav.profile') }}
                </router-link>

                <router-link to="/keys" @click="closeDropdown" class="dropdown-item">
                  <Icon name="key" size="sm" />
                  {{ t('nav.apiKeys') }}
                </router-link>

                <a
                  v-if="authStore.isAdmin"
                  href="https://github.com/JasonWangJie/sub2api_forimg"
                  target="_blank"
                  rel="noopener noreferrer"
                  @click="closeDropdown"
                  class="dropdown-item"
                >
                  <svg class="h-4 w-4" fill="currentColor" viewBox="0 0 24 24">
                    <path
                      fill-rule="evenodd"
                      clip-rule="evenodd"
                      d="M12 2C6.477 2 2 6.477 2 12c0 4.42 2.865 8.17 6.839 9.49.5.092.682-.217.682-.482 0-.237-.008-.866-.013-1.7-2.782.604-3.369-1.34-3.369-1.34-.454-1.156-1.11-1.464-1.11-1.464-.908-.62.069-.608.069-.608 1.003.07 1.531 1.03 1.531 1.03.892 1.529 2.341 1.087 2.91.831.092-.646.35-1.086.636-1.336-2.22-.253-4.555-1.11-4.555-4.943 0-1.091.39-1.984 1.029-2.683-.103-.253-.446-1.27.098-2.647 0 0 .84-.269 2.75 1.025A9.578 9.578 0 0112 6.836c.85.004 1.705.114 2.504.336 1.909-1.294 2.747-1.025 2.747-1.025.546 1.377.203 2.394.1 2.647.64.699 1.028 1.592 1.028 2.683 0 3.842-2.339 4.687-4.566 4.935.359.309.678.919.678 1.852 0 1.336-.012 2.415-.012 2.743 0 .267.18.578.688.48C19.138 20.167 22 16.418 22 12c0-5.523-4.477-10-10-10z"
                    />
                  </svg>
                  {{ t('nav.github') }}
                </a>

              </div>

              <!-- Contact Support (only show if configured) -->
              <div
                v-if="contactInfo"
                class="border-t border-sky-100/80 px-4 py-2.5 dark:border-dark-700"
              >
                <div class="flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
                  <svg
                    class="h-3.5 w-3.5 flex-shrink-0"
                    fill="none"
                    viewBox="0 0 24 24"
                    stroke="currentColor"
                    stroke-width="1.5"
                  >
                    <path
                      stroke-linecap="round"
                      stroke-linejoin="round"
                      d="M20.25 8.511c.884.284 1.5 1.128 1.5 2.097v4.286c0 1.136-.847 2.1-1.98 2.193-.34.027-.68.052-1.02.072v3.091l-3-3c-1.354 0-2.694-.055-4.02-.163a2.115 2.115 0 01-.825-.242m9.345-8.334a2.126 2.126 0 00-.476-.095 48.64 48.64 0 00-8.048 0c-1.131.094-1.976 1.057-1.976 2.192v4.286c0 .837.46 1.58 1.155 1.951m9.345-8.334V6.637c0-1.621-1.152-3.026-2.76-3.235A48.455 48.455 0 0011.25 3c-2.115 0-4.198.137-6.24.402-1.608.209-2.76 1.614-2.76 3.235v6.226c0 1.621 1.152 3.026 2.76 3.235.577.075 1.157.14 1.74.194V21l4.155-4.155"
                    />
                  </svg>
                  <span>{{ t('common.contactSupport') }}:</span>
                  <span class="font-medium text-gray-700 dark:text-gray-300">{{
                    contactInfo
                  }}</span>
                </div>
              </div>

              <div v-if="showOnboardingButton" class="border-t border-sky-100/80 py-1 dark:border-dark-700">
                <button @click="handleReplayGuide" class="dropdown-item w-full">
                  <svg class="h-4 w-4" fill="currentColor" viewBox="0 0 24 24">
                    <path
                      d="M12 2a10 10 0 100 20 10 10 0 000-20zm0 14a1 1 0 110 2 1 1 0 010-2zm1.07-7.75c0-.6-.49-1.25-1.32-1.25-.7 0-1.22.4-1.43 1.02a1 1 0 11-1.9-.62A3.41 3.41 0 0111.8 5c2.02 0 3.25 1.4 3.25 2.9 0 2-1.83 2.55-2.43 3.12-.43.4-.47.75-.47 1.23a1 1 0 01-2 0c0-1 .16-1.82 1.1-2.7.69-.64 1.82-1.05 1.82-2.06z"
                    />
                  </svg>
                  {{ $t('onboarding.restartTour') }}
                </button>
              </div>

              <div class="border-t border-sky-100/80 py-1 dark:border-dark-700">
                <button
                  @click="handleLogout"
                  class="dropdown-item w-full text-red-600 hover:bg-red-50 dark:text-red-400 dark:hover:bg-red-900/20"
                >
                  <svg
                    class="h-4 w-4"
                    fill="none"
                    viewBox="0 0 24 24"
                    stroke="currentColor"
                    stroke-width="1.5"
                  >
                    <path
                      stroke-linecap="round"
                      stroke-linejoin="round"
                      d="M15.75 9V5.25A2.25 2.25 0 0013.5 3h-6a2.25 2.25 0 00-2.25 2.25v13.5A2.25 2.25 0 007.5 21h6a2.25 2.25 0 002.25-2.25V15M12 9l-3 3m0 0l3 3m-3-3h12.75"
                    />
                  </svg>
                  {{ t('nav.logout') }}
                </button>
              </div>
            </div>
          </transition>
        </div>
      </div>
    </div>
  </header>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAppStore, useAuthStore, useOnboardingStore } from '@/stores'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import SubscriptionProgressMini from '@/components/common/SubscriptionProgressMini.vue'
import AnnouncementBell from '@/components/common/AnnouncementBell.vue'
import Icon from '@/components/icons/Icon.vue'
import { sanitizeUrl } from '@/utils/url'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'

const router = useRouter()
const route = useRoute()
const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const adminSettingsStore = useAdminSettingsStore()
const onboardingStore = useOnboardingStore()

const user = computed(() => authStore.user)
const dropdownOpen = ref(false)
const dropdownRef = ref<HTMLElement | null>(null)
const contactInfo = computed(() => appStore.contactInfo)
const docUrl = computed(() => sanitizeUrl(appStore.docUrl))
const infiniteCanvasUrl = computed(() => sanitizeUrl(appStore.infiniteCanvasUrl))
const modelPlazaEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.modelPlaza))
const avatarUrl = computed(() => user.value?.avatar_url?.trim() || '')
const availableBalance = computed(() => Number(user.value?.balance || 0))
const frozenBalance = computed(() => Number(user.value?.frozen_balance || 0))
const totalBalance = computed(() => availableBalance.value + frozenBalance.value)
const balanceAvailableText = computed(() => t('common.availableBalance') === 'common.availableBalance' ? '可用余额' : t('common.availableBalance'))
const balanceFrozenText = computed(() => t('common.frozenBalance') === 'common.frozenBalance' ? '冻结金额' : t('common.frozenBalance'))
const balanceTotalText = computed(() => t('common.totalBalance') === 'common.totalBalance' ? '总余额' : t('common.totalBalance'))
const balanceFrozenLabel = computed(() => `${balanceFrozenText.value} ${formatHeaderMoney(frozenBalance.value)}`)

// 只在标准模式的管理员下显示新手引导按钮
const showOnboardingButton = computed(() => {
  return !authStore.isSimpleMode && user.value?.role === 'admin'
})

const userInitials = computed(() => {
  if (!user.value) return ''
  // Prefer username, fallback to email
  if (user.value.username) {
    return user.value.username.substring(0, 2).toUpperCase()
  }
  if (user.value.email) {
    // Get the part before @ and take first 2 chars
    const localPart = user.value.email.split('@')[0]
    return localPart.substring(0, 2).toUpperCase()
  }
  return ''
})

const displayName = computed(() => {
  if (!user.value) return ''
  return user.value.username || user.value.email?.split('@')[0] || ''
})

const pageTitle = computed(() => {
  // For custom pages, use the menu item's label instead of generic "自定义页面"
  if (route.name === 'CustomPage') {
    const id = route.params.id as string
    const publicItems = appStore.cachedPublicSettings?.custom_menu_items ?? []
    const menuItem = publicItems.find((item) => item.id === id)
      ?? (authStore.isAdmin ? adminSettingsStore.customMenuItems.find((item) => item.id === id) : undefined)
    if (menuItem?.label) return menuItem.label
  }
  const titleKey = route.meta.titleKey as string
  if (titleKey) {
    return t(titleKey)
  }
  return (route.meta.title as string) || ''
})

const pageDescription = computed(() => {
  const descKey = route.meta.descriptionKey as string
  if (descKey) {
    return t(descKey)
  }
  return (route.meta.description as string) || ''
})

function toggleMobileSidebar() {
  appStore.toggleMobileSidebar()
}

function toggleDropdown(event: MouseEvent) {
  event.stopPropagation()
  dropdownOpen.value = !dropdownOpen.value
}

function closeDropdown() {
  dropdownOpen.value = false
}

async function handleLogout() {
  closeDropdown()
  try {
    await authStore.logout()
  } catch (error) {
    // Ignore logout errors - still redirect to login
    console.error('Logout error:', error)
  }
  await router.push('/login')
}

function handleReplayGuide() {
  closeDropdown()
  onboardingStore.replay()
}

function formatHeaderMoney(value: number) {
  if (!Number.isFinite(value)) return '$0.00'
  return `$${value.toFixed(2)}`
}

function handleClickOutside(event: MouseEvent) {
  if (dropdownRef.value && !dropdownRef.value.contains(event.target as Node)) {
    closeDropdown()
  }
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', handleClickOutside)
})
</script>

<style scoped>
.app-header {
  position: relative;
  isolation: isolate;
  overflow: visible;
  border-bottom: 1px solid rgba(186, 230, 253, 0.55);
  background:
    linear-gradient(180deg, rgba(224, 242, 254, 0.45), transparent 70%),
    linear-gradient(90deg, rgba(240, 249, 255, 0.82), rgba(255, 255, 255, 0.72) 48%, rgba(236, 254, 255, 0.55));
  backdrop-filter: blur(16px) saturate(1.2);
  box-shadow: 0 8px 24px rgba(14, 165, 233, 0.04);
}

.app-header__beam {
  position: absolute;
  left: 0;
  bottom: -1px;
  z-index: 2;
  width: min(220px, 28%);
  height: 2px;
  border-radius: 999px;
  background: linear-gradient(
    90deg,
    transparent 0%,
    rgba(56, 189, 248, 0.15) 18%,
    #38bdf8 42%,
    #e0f2fe 50%,
    #14b8a6 58%,
    rgba(45, 212, 191, 0.2) 82%,
    transparent 100%
  );
  box-shadow:
    0 0 8px rgba(56, 189, 248, 0.45),
    0 0 16px rgba(20, 184, 166, 0.25);
  animation: app-header-beam-sweep 3.6s ease-in-out infinite;
  pointer-events: none;
  will-change: left, transform;
}

.app-header__inner {
  position: relative;
  z-index: 1;
}

.app-header__title-block {
  position: relative;
  padding-left: 0.85rem;
}

.app-header__title-block::before {
  content: '';
  position: absolute;
  left: 0;
  top: 0.2rem;
  bottom: 0.2rem;
  width: 3px;
  border-radius: 999px;
  background: linear-gradient(180deg, #38bdf8, #14b8a6);
  box-shadow: 0 0 10px rgba(56, 189, 248, 0.45);
}

.app-header__title {
  margin: 0;
  color: #0f172a;
  font-size: 1.05rem;
  font-weight: 700;
  letter-spacing: -0.02em;
  line-height: 1.25;
}

.app-header__desc {
  margin: 0.15rem 0 0;
  color: #64748b;
  font-size: 0.72rem;
}

.app-header__chip {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  border: 1px solid rgba(186, 230, 253, 0.7);
  border-radius: 0.75rem;
  padding: 0.35rem 0.7rem;
  color: #475569;
  font-size: 0.8125rem;
  font-weight: 600;
  background: rgba(255, 255, 255, 0.55);
  transition:
    color 0.2s ease,
    background 0.2s ease,
    border-color 0.2s ease,
    transform 0.2s ease,
    box-shadow 0.2s ease;
}

.app-header__chip:hover {
  color: #0369a1;
  border-color: rgba(56, 189, 248, 0.55);
  background: rgba(224, 242, 254, 0.85);
  transform: translateY(-1px);
  box-shadow: 0 8px 18px rgba(14, 165, 233, 0.1);
}

.app-header__balance {
  border: 1px solid rgba(125, 211, 252, 0.55);
  border-radius: 0.85rem;
  padding: 0.35rem 0.75rem;
  background:
    linear-gradient(135deg, rgba(224, 242, 254, 0.95), rgba(204, 251, 241, 0.55));
  box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.65);
  transition:
    transform 0.2s ease,
    box-shadow 0.2s ease,
    border-color 0.2s ease;
}

.app-header__balance:hover {
  transform: translateY(-1px);
  border-color: rgba(56, 189, 248, 0.65);
  box-shadow:
    inset 0 1px 0 rgba(255, 255, 255, 0.7),
    0 8px 18px rgba(14, 165, 233, 0.12);
}

.app-header__balance-dot {
  width: 0.4rem;
  height: 0.4rem;
  border-radius: 999px;
  background: #22c55e;
  box-shadow: 0 0 0 3px rgba(34, 197, 94, 0.18);
  animation: app-header-pulse 2.4s ease-in-out infinite;
}

.app-header__balance-tip {
  z-index: 40;
  border: 1px solid rgba(186, 230, 253, 0.75);
  border-radius: 0.85rem;
  background: rgba(255, 255, 255, 0.96);
  box-shadow: 0 16px 36px rgba(15, 23, 42, 0.12);
  backdrop-filter: blur(10px);
}

.app-header__user {
  display: flex;
  align-items: center;
  gap: 0.55rem;
  border: 1px solid transparent;
  border-radius: 0.9rem;
  padding: 0.3rem 0.45rem 0.3rem 0.3rem;
  transition:
    background 0.2s ease,
    border-color 0.2s ease,
    box-shadow 0.2s ease;
}

.app-header__user:hover,
.app-header__user[aria-expanded='true'] {
  border-color: rgba(186, 230, 253, 0.7);
  background: rgba(240, 249, 255, 0.75);
  box-shadow: 0 8px 18px rgba(14, 165, 233, 0.08);
}

.app-header__avatar {
  display: flex;
  height: 2rem;
  width: 2rem;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  border-radius: 0.7rem;
  background: linear-gradient(145deg, #0ea5e9, #14b8a6);
  color: #fff;
  font-size: 0.75rem;
  font-weight: 700;
  box-shadow:
    0 0 0 2px rgba(255, 255, 255, 0.55),
    0 6px 14px rgba(14, 165, 233, 0.28);
}

.app-header__dropdown {
  z-index: 60;
  border-color: rgba(186, 230, 253, 0.65);
  box-shadow: 0 18px 40px rgba(15, 23, 42, 0.12);
}

.app-header__icon-btn {
  border: 1px solid rgba(186, 230, 253, 0.55);
  background: rgba(255, 255, 255, 0.45);
}

@keyframes app-header-beam-sweep {
  0%,
  100% {
    left: 0;
    transform: translateX(0);
  }
  50% {
    left: 100%;
    transform: translateX(-100%);
  }
}

@keyframes app-header-pulse {
  0%, 100% { box-shadow: 0 0 0 3px rgba(34, 197, 94, 0.16); }
  50% { box-shadow: 0 0 0 5px rgba(34, 197, 94, 0.08); }
}

.dropdown-enter-active,
.dropdown-leave-active {
  transition: all 0.2s ease;
}

.dropdown-enter-from,
.dropdown-leave-to {
  opacity: 0;
  transform: scale(0.95) translateY(-4px);
}

@media (prefers-reduced-motion: reduce) {
  .app-header__beam,
  .app-header__balance-dot {
    animation: none;
  }
}
</style>

<style>
/* Dark-mode overrides kept unscoped: Vue scoped compiler drops :global(.dark) in production. */
.dark .app-header {
  border-bottom-color: rgba(51, 65, 85, 0.75);
  background:
    linear-gradient(180deg, rgba(14, 165, 233, 0.12), transparent 72%),
    linear-gradient(90deg, rgba(15, 23, 42, 0.94), rgba(15, 23, 42, 0.86) 55%, rgba(12, 74, 110, 0.28));
  box-shadow: 0 10px 28px rgba(2, 6, 23, 0.28);
}

.dark .app-header__beam {
  background: linear-gradient(
    90deg,
    transparent 0%,
    rgba(56, 189, 248, 0.2) 18%,
    #38bdf8 42%,
    #7dd3fc 50%,
    #2dd4bf 58%,
    rgba(45, 212, 191, 0.25) 82%,
    transparent 100%
  );
  box-shadow:
    0 0 10px rgba(56, 189, 248, 0.55),
    0 0 18px rgba(45, 212, 191, 0.3);
}

.dark .app-header__title {
  color: #f8fafc;
}

.dark .app-header__desc {
  color: #94a3b8;
}

.dark .app-header__chip {
  color: #94a3b8;
  border-color: rgba(51, 65, 85, 0.85);
  background: rgba(15, 23, 42, 0.55);
}

.dark .app-header__chip:hover {
  color: #e0f2fe;
  border-color: rgba(56, 189, 248, 0.35);
  background: rgba(14, 165, 233, 0.16);
  box-shadow: 0 8px 18px rgba(2, 6, 23, 0.25);
}

.dark .app-header__balance {
  border-color: rgba(56, 189, 248, 0.3);
  background: linear-gradient(135deg, rgba(14, 165, 233, 0.22), rgba(20, 184, 166, 0.12));
  box-shadow: none;
}

.dark .app-header__balance:hover {
  border-color: rgba(56, 189, 248, 0.45);
  box-shadow: 0 8px 18px rgba(2, 6, 23, 0.3);
}

.dark .app-header__balance-tip {
  border-color: rgba(51, 65, 85, 0.9);
  background: rgba(15, 23, 42, 0.96);
  box-shadow: 0 16px 36px rgba(2, 6, 23, 0.45);
}

.dark .app-header__user:hover,
.dark .app-header__user[aria-expanded='true'] {
  border-color: rgba(56, 189, 248, 0.28);
  background: rgba(30, 41, 59, 0.65);
  box-shadow: none;
}

.dark .app-header__avatar {
  box-shadow:
    0 0 0 2px rgba(15, 23, 42, 0.85),
    0 6px 14px rgba(14, 165, 233, 0.28);
}

.dark .app-header__dropdown {
  border-color: rgba(51, 65, 85, 0.9);
  box-shadow: 0 18px 40px rgba(2, 6, 23, 0.45);
}

.dark .app-header__icon-btn {
  border-color: rgba(51, 65, 85, 0.85);
  background: rgba(15, 23, 42, 0.55);
  color: #e2e8f0;
}
</style>
