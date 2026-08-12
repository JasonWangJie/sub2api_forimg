import type { ApiKey } from '@/types'
import type {
  ImageAsyncProtocol,
  ImageExecutionMode,
  ImageModelCapability,
  ImageWorkbenchCapabilities,
  ImageWorkbenchPlatform,
  WorkbenchAccess,
} from './types'

const WORKBENCH_PLATFORMS = new Set<ImageWorkbenchPlatform>(['openai', 'gemini', 'grok'])

export function deriveWorkbenchAccess(key: ApiKey): WorkbenchAccess {
  if (key.status !== 'active') {
    return { supported: false, platform: null, mode: null, protocol: null, reason: 'inactive' }
  }
  if (!key.group?.allow_image_generation) {
    return { supported: false, platform: null, mode: null, protocol: null, reason: 'image_disabled' }
  }

  const platform = String(key.group.platform || '').toLowerCase() as ImageWorkbenchPlatform
  if (!WORKBENCH_PLATFORMS.has(platform)) {
    return { supported: false, platform: null, mode: null, protocol: null, reason: 'unsupported_platform' }
  }

  const asyncEnabled = platform !== 'grok' && key.group.allow_async_image_generation === true
  return {
    supported: true,
    platform,
    mode: asyncEnabled ? 'async' : 'realtime',
    protocol: asyncEnabled ? (platform === 'gemini' ? 'sc' : 'bb') : null,
  }
}

function stringArray(value: unknown): string[] {
  if (!Array.isArray(value)) return []
  return value.map((item) => String(item || '').trim()).filter(Boolean)
}

function normalizeModels(value: unknown): ImageModelCapability[] {
  if (!Array.isArray(value)) return []
  return value
    .map((item): ImageModelCapability | null => {
      if (typeof item === 'string') {
        const id = item.trim()
        return id ? { id, label: id } : null
      }
      if (!item || typeof item !== 'object') return null
      const source = item as Record<string, unknown>
      const id = String(source.id || source.value || source.model || source.name || '').trim()
      if (!id) return null
      return { id, label: String(source.label || source.display_name || source.name || id) }
    })
    .filter((item): item is ImageModelCapability => item !== null)
}

function normalizeMode(value: unknown, fallback: ImageExecutionMode): ImageExecutionMode {
  const mode = String(value || '').toLowerCase()
  return mode === 'async' || mode === 'asynchronous' ? 'async' : mode === 'realtime' || mode === 'sync' ? 'realtime' : fallback
}

function normalizeProtocol(value: unknown, platform: ImageWorkbenchPlatform, mode: ImageExecutionMode): ImageAsyncProtocol | null {
  if (mode !== 'async') return null
  const protocol = String(value || '').toLowerCase()
  if (protocol === 'bb' || protocol === 'sc') return protocol
  return platform === 'gemini' ? 'sc' : 'bb'
}

export function normalizeCapabilities(
  wire: unknown,
  key: ApiKey,
): ImageWorkbenchCapabilities {
  const access = deriveWorkbenchAccess(key)
  if (!access.supported || !access.platform || !access.mode) {
    throw new Error('Selected API key is not image-capable')
  }

  const root = (wire && typeof wire === 'object' ? wire : {}) as Record<string, unknown>
  const data = (root.capabilities && typeof root.capabilities === 'object'
    ? root.capabilities
    : root) as Record<string, unknown>
  if (data.available === false) {
    throw new Error(String(data.unavailable_reason || 'Selected API key is not available for image generation'))
  }
  const platform = String(data.platform || access.platform).toLowerCase() as ImageWorkbenchPlatform
  if (!WORKBENCH_PLATFORMS.has(platform)) throw new Error('Unsupported image platform')
  const executionMode = normalizeMode(data.execution_mode ?? data.mode, access.mode)
  const models = normalizeModels(data.models ?? data.available_models ?? data.model_options)

  return {
    api_key_id: Number(data.api_key_id || key.id),
    group_id: data.group_id == null ? key.group_id : Number(data.group_id),
    capability_version: String(data.capability_version || data.version || `${key.group_id || 0}:${key.group?.updated_at || key.updated_at}:${platform}:${executionMode}`),
    platform,
    execution_mode: executionMode,
    async_protocol: normalizeProtocol(data.async_protocol ?? data.protocol, platform, executionMode),
    // Field aliases support older capability schemas. Missing or explicitly
    // empty fields stay empty so the client never advertises server-disabled features.
    models,
    sizes: stringArray(data.sizes ?? (platform === 'openai' && Array.isArray(data.aspect_ratios) && data.aspect_ratios.length
      ? []
      : data.image_sizes)),
    resolutions: stringArray(
      data.resolutions
      ?? data.image_resolutions
      ?? ((platform === 'gemini' || platform === 'openai') ? data.image_sizes : []),
    ),
    aspect_ratios: stringArray(data.aspect_ratios),
    qualities: stringArray(data.qualities),
    output_formats: stringArray(data.output_formats ?? data.formats),
    backgrounds: stringArray(data.backgrounds),
    styles: stringArray(data.styles),
    max_images: Math.max(1, Number(data.max_output_images ?? data.max_images ?? data.max_image_count ?? 1) || 1),
    max_reference_images: Math.max(0, Number(data.max_reference_images ?? 0) || 0),
    supports_reference_images: data.supports_reference_images === true,
  }
}

export function sameCapabilitySnapshot(
  previous: ImageWorkbenchCapabilities,
  current: ImageWorkbenchCapabilities,
): boolean {
  return previous.api_key_id === current.api_key_id
    && previous.group_id === current.group_id
    && previous.capability_version === current.capability_version
    && previous.platform === current.platform
    && previous.execution_mode === current.execution_mode
    && previous.async_protocol === current.async_protocol
}
