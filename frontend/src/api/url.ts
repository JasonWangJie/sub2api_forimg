const DEFAULT_API_BASE_URL = '/api/v1'
const API_BASE_URL = normalizeAPIBaseURL(import.meta.env.VITE_API_BASE_URL)

function normalizePath(path: string): string {
  return path.startsWith('/') ? path : `/${path}`
}

function normalizeAPIBaseURL(value: unknown): string {
  const raw = String(value || DEFAULT_API_BASE_URL).trim() || DEFAULT_API_BASE_URL
  const withoutTrailingSlash = raw.replace(/\/+$/, '')
  if (/^[a-z][a-z\d+.-]*:\/\//i.test(withoutTrailingSlash) || withoutTrailingSlash.startsWith('//')) {
    return withoutTrailingSlash
  }
  return normalizePath(withoutTrailingSlash)
}

export function getAPIBaseURL(): string {
  return API_BASE_URL
}

export function buildApiUrl(path: string): string {
  const base = getAPIBaseURL().replace(/\/+$/, '')
  let suffix = normalizePath(path)
  if (suffix === DEFAULT_API_BASE_URL) {
    suffix = ''
  } else if (suffix.startsWith(`${DEFAULT_API_BASE_URL}/`)) {
    suffix = suffix.slice(DEFAULT_API_BASE_URL.length)
  }
  return `${base}${suffix}`
}

/**
 * Build an absolute gateway URL for OpenAI-compatible routes (/v1/...) and other
 * same-origin gateway endpoints. Always prefers the current site origin so the
 * workbench/batch clients hit `{site}/v1/...` instead of a remote API host.
 */
export function buildGatewayUrl(path: string): string {
  const suffix = normalizePath(path)
  if (typeof window !== 'undefined' && window.location?.origin) {
    return `${window.location.origin}${suffix}`
  }
  try {
    // Non-browser fallback (tests / SSR): keep previous absolute-base behavior.
    const origin = new URL(getAPIBaseURL(), 'http://localhost').origin
    return `${origin}${suffix}`
  } catch {
    return suffix
  }
}

/** Site gateway base, e.g. https://example.com/v1 */
export function getSiteGatewayBase(): string {
  if (typeof window !== 'undefined' && window.location?.origin) {
    return `${window.location.origin}/v1`
  }
  try {
    return `${new URL(getAPIBaseURL(), 'http://localhost').origin}/v1`
  } catch {
    return '/v1'
  }
}
