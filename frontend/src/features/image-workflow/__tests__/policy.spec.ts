import { describe, expect, it } from 'vitest'
import { deriveWorkbenchAccess, normalizeCapabilities, sameCapabilitySnapshot } from '../policy'
import type { ApiKey, GroupPlatform } from '@/types'

function key(platform: GroupPlatform, asyncEnabled: boolean): ApiKey {
  return {
    id: 7,
    status: 'active',
    group_id: 11,
    updated_at: '2026-07-21T00:00:00Z',
    group: {
      id: 11,
      platform,
      allow_image_generation: true,
      allow_async_image_generation: asyncEnabled,
      updated_at: '2026-07-21T00:00:00Z',
    },
  } as ApiKey
}

describe('image workbench execution policy', () => {
  it.each([
    ['OpenAI realtime', 'openai', false, 'realtime', null],
    ['OpenAI async', 'openai', true, 'async', 'bb'],
    ['Gemini realtime', 'gemini', false, 'realtime', null],
    ['Gemini async', 'gemini', true, 'async', 'sc'],
    ['Grok realtime even if an invalid async flag leaks through', 'grok', true, 'realtime', null],
  ] as const)('%s', (_name, platform, asyncEnabled, mode, protocol) => {
    expect(deriveWorkbenchAccess(key(platform, asyncEnabled))).toMatchObject({
      supported: true,
      platform,
      mode,
      protocol,
    })
  })

  it('hides Antigravity and non-image groups from the workbench', () => {
    expect(deriveWorkbenchAccess(key('antigravity', true))).toMatchObject({
      supported: false,
      reason: 'unsupported_platform',
    })
    const disabled = key('openai', false)
    disabled.group!.allow_image_generation = false
    expect(deriveWorkbenchAccess(disabled).supported).toBe(false)
  })

  it('normalizes the server capability contract without inventing Grok parameters', () => {
    const capabilities = normalizeCapabilities({
      capability_version: 'cap-v2',
      platform: 'grok',
      execution_mode: 'realtime',
      protocol: 'grok_images',
      models: [{ id: 'grok-2-image-1212', label: 'Grok Image' }],
      image_sizes: [],
      qualities: [],
      formats: [],
      max_output_images: 1,
      max_reference_images: 1,
    }, key('grok', false))

    expect(capabilities.execution_mode).toBe('realtime')
    expect(capabilities.sizes).toEqual([])
    expect(capabilities.qualities).toEqual([])
    expect(capabilities.output_formats).toEqual([])
    expect(capabilities.max_images).toBe(1)
  })

  it('preserves explicit empty capabilities instead of injecting platform defaults', () => {
    const capabilities = normalizeCapabilities({
      capability_version: 'empty-openai',
      platform: 'openai',
      execution_mode: 'realtime',
      models: [],
      image_sizes: [],
      qualities: [],
      formats: [],
      backgrounds: [],
      max_output_images: 1,
      max_reference_images: 0,
      supports_reference_images: false,
    }, key('openai', false))

    expect(capabilities.models).toEqual([])
    expect(capabilities.sizes).toEqual([])
    expect(capabilities.qualities).toEqual([])
    expect(capabilities.output_formats).toEqual([])
    expect(capabilities.backgrounds).toEqual([])
    expect(capabilities.max_reference_images).toBe(0)
    expect(capabilities.supports_reference_images).toBe(false)
  })

  it('accepts legacy field aliases without inventing missing capabilities', () => {
    const capabilities = normalizeCapabilities({
      version: 'legacy-capability',
      available_models: ['gpt-image-custom'],
      image_sizes: ['1024x1024'],
      formats: ['png'],
    }, key('openai', false))

    expect(capabilities.models).toEqual([{ id: 'gpt-image-custom', label: 'gpt-image-custom' }])
    expect(capabilities.sizes).toEqual(['1024x1024'])
    expect(capabilities.output_formats).toEqual(['png'])
    expect(capabilities.qualities).toEqual([])
    expect(capabilities.backgrounds).toEqual([])
    expect(capabilities.max_images).toBe(1)
    expect(capabilities.max_reference_images).toBe(0)
    expect(capabilities.supports_reference_images).toBe(false)
  })

  it('stops submission when the capability snapshot changes', () => {
    const original = normalizeCapabilities({ capability_version: 'one' }, key('openai', false))
    const current = normalizeCapabilities({ capability_version: 'two' }, key('openai', false))
    expect(sameCapabilitySnapshot(original, current)).toBe(false)
  })

  it('does not invent availability when the server rejects the current key context', () => {
    expect(() => normalizeCapabilities({
      available: false,
      unavailable_reason: 'group_inactive',
    }, key('openai', false))).toThrow('group_inactive')
  })
})
