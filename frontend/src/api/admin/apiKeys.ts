/**
 * Admin API Keys API endpoints
 * Handles API key management for administrators
 */

import { apiClient } from '../client'
import type { ApiKey } from '@/types'

export interface UpdateApiKeyGroupResult {
  api_key: ApiKey
  auto_granted_group_access: boolean
  granted_group_id?: number
  granted_group_name?: string
}

export interface UpdateApiKeyAdminPayload {
  /** undefined = no change; null = unbind; number = bind */
  group_id?: number | null
  image_platform_groups?: Record<string, number>
  reset_rate_limit_usage?: boolean
}

/**
 * Update an API key's admin-managed bindings.
 */
export async function updateApiKeyGroup(
  id: number,
  groupId?: number | null,
  imagePlatformGroups?: Record<string, number>
): Promise<UpdateApiKeyGroupResult> {
  const payload: UpdateApiKeyAdminPayload = {}
  if (groupId !== undefined) {
    payload.group_id = groupId === null ? 0 : groupId
  }
  if (imagePlatformGroups !== undefined) {
    payload.image_platform_groups = imagePlatformGroups
  }
  const { data } = await apiClient.put<UpdateApiKeyGroupResult>(`/admin/api-keys/${id}`, payload)
  return data
}

export const apiKeysAPI = {
  updateApiKeyGroup
}

export default apiKeysAPI
