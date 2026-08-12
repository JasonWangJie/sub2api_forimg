/**
 * Build vendor-specific image process URLs for grid thumbnails.
 * Works on permanent / CDN object URLs. Presigned SigV4 URLs are left unchanged
 * because appending process params would invalidate the signature.
 */

export type OssThumbnailProvider = 'aliyun' | 'qiniu' | 'tencent' | 'custom_s3' | string

export interface OssThumbnailOptions {
  /** Long-edge / width target in CSS pixels. Default 480. */
  width?: number
  /** Explicit storage provider; otherwise inferred from the URL host. */
  provider?: OssThumbnailProvider | null
}

const DEFAULT_WIDTH = 480

export function inferOssProvider(url: string): OssThumbnailProvider | null {
  try {
    const host = new URL(url).hostname.toLowerCase()
    if (host.includes('aliyuncs.com') || host.includes('aliyun') || host.includes('oss-')) return 'aliyun'
    if (host.includes('qiniucdn.com') || host.includes('qiniudn.com') || host.includes('clouddn.com') || host.includes('qiniu')) {
      return 'qiniu'
    }
    if (host.includes('myqcloud.com') || host.includes('tencentcos') || host.includes('cos.')) return 'tencent'
  } catch {
    return null
  }
  return null
}

function isPresignedObjectURL(url: string): boolean {
  return /[?&](X-Amz-Signature|X-Amz-Credential|Signature|OSSAccessKeyId|q-signature)=/i.test(url)
}

function appendQuery(url: string, query: string): string {
  const joiner = url.includes('?') ? '&' : '?'
  return `${url}${joiner}${query}`
}

function appendPathProcess(url: string, process: string): string {
  // Qiniu style: URL?imageView2/...  or URL/imageView2/...
  // Prefer query form which works with most CDN setups.
  const joiner = url.includes('?') ? '|' : '?'
  return `${url}${joiner}${process}`
}

/**
 * Return a thumbnail URL for list/grid display. Falls back to the original URL
 * when the vendor cannot be determined or the URL is already signed for GET-only.
 */
export function buildOssThumbnailUrl(rawURL: string, options: OssThumbnailOptions = {}): string {
  const url = String(rawURL || '').trim()
  if (!url) return ''
  if (url.startsWith('data:') || url.startsWith('blob:') || url.startsWith('/')) return url
  if (isPresignedObjectURL(url)) return url

  const width = Math.max(64, Math.min(2048, Math.round(options.width || DEFAULT_WIDTH)))
  const provider = String(options.provider || inferOssProvider(url) || '')
    .trim()
    .toLowerCase()

  switch (provider) {
    case 'aliyun':
      return appendQuery(url, `x-oss-process=image/resize,m_lfit,w_${width}`)
    case 'qiniu':
      return appendPathProcess(url, `imageView2/2/w/${width}`)
    case 'tencent':
      return appendQuery(url, `imageMogr2/thumbnail/${width}x`)
    default:
      // Unknown / custom_s3: keep original; CSS object-fit handles visual sizing.
      return url
  }
}
