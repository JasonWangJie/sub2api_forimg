<template>
  <AppLayout>
    <div class="dashboard-page space-y-6">
      <header class="dashboard-hero">
        <div class="dashboard-hero__glow" aria-hidden="true"></div>
        <div class="min-w-0">
          <p class="dashboard-hero__eyebrow">{{ t('dashboard.title') }}</p>
          <h2 class="dashboard-hero__title">
            {{ greeting }}
            <span v-if="displayName" class="dashboard-hero__name">{{ displayName }}</span>
          </h2>
          <p class="dashboard-hero__desc">{{ t('dashboard.welcomeMessage') }}</p>
        </div>
        <div class="dashboard-hero__meta">
          <span class="dashboard-hero__chip">{{ todayLabel }}</span>
          <button type="button" class="btn btn-secondary dashboard-hero__refresh" :disabled="loading || loadingCharts" @click="refreshAll">
            {{ t('common.refresh') }}
          </button>
        </div>
      </header>

      <div v-if="loading" class="flex items-center justify-center py-12"><LoadingSpinner /></div>
      <template v-else-if="stats">
        <section class="dashboard-section dashboard-section--stats">
          <UserDashboardStats :stats="stats" :balance="user?.balance || 0" :is-simple="authStore.isSimpleMode" :platform-quotas="platformQuotas" />
        </section>
        <section class="dashboard-section">
          <UserDashboardCharts v-model:startDate="startDate" v-model:endDate="endDate" v-model:granularity="granularity" :loading="loadingCharts" :trend="trendData" :models="modelStats" @dateRangeChange="loadCharts" @granularityChange="loadCharts" @refresh="refreshAll" />
        </section>
        <section class="dashboard-section grid grid-cols-1 gap-6 lg:grid-cols-3">
          <div class="lg:col-span-2"><UserDashboardRecentUsage :data="recentUsage" :loading="loadingUsage" /></div>
          <div class="lg:col-span-1"><UserDashboardQuickActions /></div>
        </section>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { usageAPI, type UserDashboardStats as UserStatsType } from '@/api/usage'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import UserDashboardStats from '@/components/user/dashboard/UserDashboardStats.vue'
import UserDashboardCharts from '@/components/user/dashboard/UserDashboardCharts.vue'
import UserDashboardRecentUsage from '@/components/user/dashboard/UserDashboardRecentUsage.vue'
import UserDashboardQuickActions from '@/components/user/dashboard/UserDashboardQuickActions.vue'
import type { UsageLog, TrendDataPoint, ModelStat, PlatformQuotaItem } from '@/types'
import { getMyPlatformQuotas } from '@/api/user'
import { formatDateLocalInput } from '@/utils/format'

const { t, locale } = useI18n()
const authStore = useAuthStore()
const user = computed(() => authStore.user)
const stats = ref<UserStatsType | null>(null)
const loading = ref(false)
const loadingUsage = ref(false)
const loadingCharts = ref(false)
const trendData = ref<TrendDataPoint[]>([])
const modelStats = ref<ModelStat[]>([])
const recentUsage = ref<UsageLog[]>([])
const platformQuotas = ref<PlatformQuotaItem[] | null>(null)

const startDate = ref(formatDateLocalInput(new Date(Date.now() - 6 * 86400000)))
const endDate = ref(formatDateLocalInput(new Date()))
const granularity = ref('day')

const displayName = computed(() => {
  const rawUser = user.value?.username
  const raw = typeof rawUser === 'string'
    ? rawUser.trim()
    : String(user.value?.email || '').trim()
  if (!raw) return ''
  if (raw.includes('@')) return raw.split('@')[0]
  return raw
})

const greeting = computed(() => {
  const hour = new Date().getHours()
  if (hour < 12) return t('dashboard.greetingMorning')
  if (hour < 18) return t('dashboard.greetingAfternoon')
  return t('dashboard.greetingEvening')
})

const todayLabel = computed(() => {
  try {
    return new Intl.DateTimeFormat(locale.value || undefined, {
      weekday: 'short',
      month: 'short',
      day: 'numeric',
    }).format(new Date())
  } catch {
    return formatDateLocalInput(new Date())
  }
})

const loadStats = async () => {
  loading.value = true
  try {
    await authStore.refreshUser()
    stats.value = await usageAPI.getDashboardStats()
  } catch (error) {
    console.error('Failed to load dashboard stats:', error)
  } finally {
    loading.value = false
  }
}
const loadCharts = async () => {
  loadingCharts.value = true
  try {
    const res = await Promise.all([
      usageAPI.getDashboardTrend({ start_date: startDate.value, end_date: endDate.value, granularity: granularity.value as any }),
      usageAPI.getDashboardModels({ start_date: startDate.value, end_date: endDate.value }),
    ])
    trendData.value = res[0].trend || []
    modelStats.value = res[1].models || []
  } catch (error) {
    console.error('Failed to load charts:', error)
  } finally {
    loadingCharts.value = false
  }
}
const loadRecent = async () => {
  loadingUsage.value = true
  try {
    const res = await usageAPI.getByDateRange(startDate.value, endDate.value)
    recentUsage.value = res.items.slice(0, 5)
  } catch (error) {
    console.error('Failed to load recent usage:', error)
  } finally {
    loadingUsage.value = false
  }
}
const loadPlatformQuotas = async () => {
  try {
    const data = await getMyPlatformQuotas()
    platformQuotas.value = data.platform_quotas ?? []
  } catch (error) {
    console.warn('Failed to load platform quotas:', error)
    platformQuotas.value = []
  }
}
const refreshAll = () => {
  loadStats()
  loadCharts()
  loadRecent()
  loadPlatformQuotas()
}

onMounted(() => {
  refreshAll()
})
</script>

<style scoped>
@property --dash-card-angle {
  syntax: '<angle>';
  inherits: false;
  initial-value: 0deg;
}

.dashboard-page {
  position: relative;
}

.dashboard-hero {
  position: relative;
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 1rem;
  overflow: hidden;
  padding: 1.15rem 1.25rem;
  border: 1px solid rgba(186, 230, 253, 0.75);
  border-radius: 1.1rem;
  background:
    linear-gradient(135deg, rgba(240, 249, 255, 0.95) 0%, rgba(236, 254, 255, 0.88) 48%, rgba(255, 255, 255, 0.96) 100%);
  box-shadow: 0 10px 30px rgba(14, 165, 233, 0.06);
}

.dark .dashboard-hero {
  border-color: rgba(51, 65, 85, 0.85);
  background:
    linear-gradient(135deg, rgba(15, 23, 42, 0.92) 0%, rgba(12, 74, 110, 0.28) 42%, rgba(15, 23, 42, 0.95) 100%);
  box-shadow: 0 12px 28px rgba(2, 6, 23, 0.35);
}

.dashboard-hero__glow {
  position: absolute;
  right: -3rem;
  top: -4rem;
  width: 14rem;
  height: 14rem;
  border-radius: 999px;
  background: radial-gradient(circle, rgba(56, 189, 248, 0.28), transparent 68%);
  pointer-events: none;
}

.dark .dashboard-hero__glow {
  background: radial-gradient(circle, rgba(14, 165, 233, 0.22), transparent 68%);
}

.dashboard-hero__eyebrow {
  margin: 0;
  color: #0284c7;
  font-size: 0.72rem;
  font-weight: 700;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}

.dark .dashboard-hero__eyebrow {
  color: #7dd3fc;
}

.dashboard-hero__title {
  margin: 0.35rem 0 0;
  color: #0f172a;
  font-size: 1.35rem;
  font-weight: 750;
  letter-spacing: -0.02em;
  line-height: 1.3;
}

.dark .dashboard-hero__title {
  color: #f8fafc;
}

.dashboard-hero__name {
  background: linear-gradient(90deg, #0284c7, #0d9488);
  -webkit-background-clip: text;
  background-clip: text;
  color: transparent;
}

.dashboard-hero__desc {
  margin: 0.35rem 0 0;
  max-width: 40rem;
  color: #64748b;
  font-size: 0.875rem;
}

.dark .dashboard-hero__desc {
  color: #94a3b8;
}

.dashboard-hero__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 0.55rem;
  flex: 0 0 auto;
}

.dashboard-hero__chip {
  display: inline-flex;
  align-items: center;
  padding: 0.35rem 0.7rem;
  border: 1px solid rgba(125, 211, 252, 0.55);
  border-radius: 999px;
  background: rgba(255, 255, 255, 0.7);
  color: #0369a1;
  font-size: 0.72rem;
  font-weight: 650;
}

.dark .dashboard-hero__chip {
  border-color: rgba(56, 189, 248, 0.28);
  background: rgba(15, 23, 42, 0.55);
  color: #7dd3fc;
}

.dashboard-section--stats :deep(.card) {
  --dash-card-angle: 0deg;
  position: relative;
  isolation: isolate;
  overflow: hidden;
  border-color: rgba(186, 230, 253, 0.4);
  background:
    linear-gradient(180deg, rgba(255, 255, 255, 0.98), rgba(248, 250, 252, 0.94));
  transition:
    transform 0.22s ease,
    box-shadow 0.22s ease,
    border-color 0.22s ease;
}

.dark .dashboard-section--stats :deep(.card) {
  border-color: rgba(51, 65, 85, 0.75);
  background: linear-gradient(180deg, rgba(30, 41, 59, 0.55), rgba(15, 23, 42, 0.45));
}

.dashboard-section--stats :deep(.card > *) {
  position: relative;
  z-index: 1;
}

/* 边框流光：沿卡片轮廓绕行 */
.dashboard-section--stats :deep(.card::before) {
  content: '';
  position: absolute;
  inset: 0;
  z-index: 2;
  border-radius: inherit;
  padding: 1.5px;
  pointer-events: none;
  background: conic-gradient(
    from var(--dash-card-angle),
    transparent 0%,
    transparent 58%,
    rgba(56, 189, 248, 0.05) 66%,
    rgba(56, 189, 248, 0.55) 74%,
    #e0f2fe 80%,
    #38bdf8 84%,
    #14b8a6 90%,
    transparent 97%,
    transparent 100%
  );
  -webkit-mask:
    linear-gradient(#fff 0 0) content-box,
    linear-gradient(#fff 0 0);
  -webkit-mask-composite: xor;
  mask-composite: exclude;
  opacity: 0.9;
  animation: dash-card-border-spin 3.6s linear infinite;
}

.dark .dashboard-section--stats :deep(.card::before) {
  background: conic-gradient(
    from var(--dash-card-angle),
    transparent 0%,
    transparent 58%,
    rgba(56, 189, 248, 0.08) 66%,
    rgba(56, 189, 248, 0.65) 74%,
    #7dd3fc 80%,
    #38bdf8 84%,
    #2dd4bf 90%,
    transparent 97%,
    transparent 100%
  );
  opacity: 1;
}

.dashboard-section--stats :deep(.card:nth-child(2)::before) {
  animation-delay: -0.7s;
}

.dashboard-section--stats :deep(.card:nth-child(3)::before) {
  animation-delay: -1.4s;
}

.dashboard-section--stats :deep(.card:nth-child(4)::before) {
  animation-delay: -2.1s;
}

.dashboard-section--stats :deep(.card:hover) {
  transform: translateY(-2px);
  border-color: rgba(56, 189, 248, 0.35);
  box-shadow:
    0 12px 28px rgba(14, 165, 233, 0.08),
    0 0 0 1px rgba(56, 189, 248, 0.12);
}

.dashboard-section--stats :deep(.card:hover::before) {
  animation-duration: 2.2s;
  filter: brightness(1.2) saturate(1.15);
}

.dark .dashboard-section--stats :deep(.card:hover) {
  border-color: rgba(56, 189, 248, 0.28);
  box-shadow:
    0 14px 28px rgba(2, 6, 23, 0.35),
    0 0 18px rgba(56, 189, 248, 0.12);
}

.dashboard-section {
  animation: dashboard-rise 0.45s ease both;
}

.dashboard-section--stats {
  animation-delay: 0.04s;
}

.dashboard-section:nth-of-type(2) {
  animation-delay: 0.1s;
}

.dashboard-section:nth-of-type(3) {
  animation-delay: 0.16s;
}

@keyframes dashboard-rise {
  from {
    opacity: 0;
    transform: translateY(8px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

@keyframes dash-card-border-spin {
  to {
    --dash-card-angle: 360deg;
  }
}

@media (max-width: 768px) {
  .dashboard-hero {
    flex-direction: column;
    align-items: flex-start;
  }

  .dashboard-hero__meta {
    width: 100%;
    justify-content: flex-start;
  }
}

@media (prefers-reduced-motion: reduce) {
  .dashboard-section {
    animation: none;
  }

  .dashboard-section--stats :deep(.card:hover) {
    transform: none;
  }

  .dashboard-section--stats :deep(.card::before) {
    animation: none;
    --dash-card-angle: 210deg;
    opacity: 0.55;
  }
}
</style>
