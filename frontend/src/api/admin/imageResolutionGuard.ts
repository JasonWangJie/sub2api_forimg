import { apiClient } from '../client'
import type { ImageResolutionCooldown } from '@/types'
export type { ImageResolutionCooldown } from '@/types'

export interface ImageResolutionGuardSettings {
  enabled: boolean
  min_area_percent: number
  failure_threshold: number
  cooldown_seconds: number
  email_enabled: boolean
  recipients: string[]
}

export interface ImageResolutionGuardSettingsView extends ImageResolutionGuardSettings {
  effective_recipients: string[]
  recipient_source: 'override' | 'admin' | 'none'
  smtp_configured: boolean
  warnings: string[]
}

export const defaultImageResolutionGuardSettings = (): ImageResolutionGuardSettings => ({
  enabled: false,
  min_area_percent: 75,
  failure_threshold: 1,
  cooldown_seconds: 600,
  email_enabled: true,
  recipients: []
})

export async function getImageResolutionGuardSettings(): Promise<ImageResolutionGuardSettingsView> {
  const { data } = await apiClient.get<ImageResolutionGuardSettingsView>('/admin/settings/image-resolution-guard')
  return data
}

export async function updateImageResolutionGuardSettings(settings: ImageResolutionGuardSettings): Promise<ImageResolutionGuardSettingsView> {
  const { enabled, min_area_percent, failure_threshold, cooldown_seconds, email_enabled, recipients } = settings
  const { data } = await apiClient.put<ImageResolutionGuardSettingsView>('/admin/settings/image-resolution-guard', {
    enabled, min_area_percent, failure_threshold, cooldown_seconds, email_enabled, recipients
  })
  return data
}

export async function getImageResolutionCooldowns(accountID: number): Promise<ImageResolutionCooldown[]> {
  const { data } = await apiClient.get<{ items: ImageResolutionCooldown[] }>(`/admin/accounts/${accountID}/image-resolution-cooldowns`)
  return data.items ?? []
}

export async function clearImageResolutionCooldown(accountID: number, cycleID: string): Promise<void> {
  await apiClient.post(`/admin/accounts/${accountID}/image-resolution-cooldowns/${encodeURIComponent(cycleID)}/clear`)
}
