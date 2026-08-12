import { describe, expect, it } from 'vitest'
import { buildOssThumbnailUrl, inferOssProvider } from '../ossThumbnail'

describe('ossThumbnail', () => {
  it('infers providers from common CDN hosts', () => {
    expect(inferOssProvider('https://bucket.oss-cn-chengdu.aliyuncs.com/a.png')).toBe('aliyun')
    expect(inferOssProvider('https://cdn.qiniucdn.com/a.png')).toBe('qiniu')
    expect(inferOssProvider('https://bucket.cos.ap-shanghai.myqcloud.com/a.png')).toBe('tencent')
  })

  it('builds aliyun / qiniu / tencent process URLs', () => {
    expect(buildOssThumbnailUrl('https://bucket.oss-cn-chengdu.aliyuncs.com/a.png', { width: 320 })).toBe(
      'https://bucket.oss-cn-chengdu.aliyuncs.com/a.png?x-oss-process=image/resize,m_lfit,w_320',
    )
    expect(buildOssThumbnailUrl('https://cdn.qiniucdn.com/a.png', { provider: 'qiniu', width: 200 })).toBe(
      'https://cdn.qiniucdn.com/a.png?imageView2/2/w/200',
    )
    expect(buildOssThumbnailUrl('https://bucket.cos.ap-shanghai.myqcloud.com/a.png', { width: 400 })).toBe(
      'https://bucket.cos.ap-shanghai.myqcloud.com/a.png?imageMogr2/thumbnail/400x',
    )
  })

  it('leaves data/blob/relative and presigned URLs unchanged', () => {
    expect(buildOssThumbnailUrl('data:image/png;base64,aaa')).toBe('data:image/png;base64,aaa')
    expect(buildOssThumbnailUrl('/api/v1/user/image-library/img_1/view')).toBe('/api/v1/user/image-library/img_1/view')
    const signed = 'https://bucket.s3.amazonaws.com/a.png?X-Amz-Signature=abc'
    expect(buildOssThumbnailUrl(signed, { provider: 'aliyun' })).toBe(signed)
  })
})
