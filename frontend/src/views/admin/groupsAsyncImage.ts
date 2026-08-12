import type { GroupPlatform } from '@/types'

export interface AsyncImageGroupToggleState {
  platform: GroupPlatform
  allow_image_generation: boolean
  allow_async_image_generation: boolean
}

export function supportsAsyncImageGeneration(platform: GroupPlatform): boolean {
  return platform === 'gemini' || platform === 'openai'
}

export function resetDisabledAsyncImageGeneration<T extends AsyncImageGroupToggleState>(form: T): void {
  if (!supportsAsyncImageGeneration(form.platform) || !form.allow_image_generation) {
    form.allow_async_image_generation = false
  }
}
