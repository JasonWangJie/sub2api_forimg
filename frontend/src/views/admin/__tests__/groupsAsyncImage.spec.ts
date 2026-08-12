import { describe, expect, it } from 'vitest'

import {
  resetDisabledAsyncImageGeneration,
  supportsAsyncImageGeneration,
} from '../groupsAsyncImage'

describe('groups async image toggle', () => {
  it('supports Gemini and OpenAI groups only', () => {
    expect(supportsAsyncImageGeneration('gemini')).toBe(true)
    expect(supportsAsyncImageGeneration('openai')).toBe(true)
    expect(supportsAsyncImageGeneration('anthropic')).toBe(false)
    expect(supportsAsyncImageGeneration('grok')).toBe(false)
  })

  it('turns async image generation off with parent image generation', () => {
    const state = {
      platform: 'gemini' as const,
      allow_image_generation: false,
      allow_async_image_generation: true,
    }
    resetDisabledAsyncImageGeneration(state)
    expect(state.allow_async_image_generation).toBe(false)
  })

  it('turns async image generation off after switching to another platform', () => {
    const state = {
      platform: 'anthropic' as const,
      allow_image_generation: true,
      allow_async_image_generation: true,
    }
    resetDisabledAsyncImageGeneration(state)
    expect(state.allow_async_image_generation).toBe(false)
  })

  it('preserves an enabled valid group', () => {
    const state = {
      platform: 'openai' as const,
      allow_image_generation: true,
      allow_async_image_generation: true,
    }
    resetDisabledAsyncImageGeneration(state)
    expect(state.allow_async_image_generation).toBe(true)
  })
})
