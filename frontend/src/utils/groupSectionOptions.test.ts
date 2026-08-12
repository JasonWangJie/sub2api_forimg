import { describe, expect, it } from 'vitest'
import {
  filterSectionedGroupOptions,
  withGroupSectionHeaders
} from '@/utils/groupSectionOptions'

describe('groupSectionOptions', () => {
  const items = [
    { value: 1, label: 'img-a', description: 'a', section: '图片分组' },
    { value: 2, label: 'img-b', description: null, section: '图片分组' },
    { value: 3, label: 'vid-a', description: 'video', section: '视频分组' },
    { value: 4, label: 'plain', description: 'no section', section: '' }
  ]

  it('inserts section headers and keeps empty section last when already ordered', () => {
    const options = withGroupSectionHeaders(items, '未分类')
    expect(options.map((o) => o.label)).toEqual([
      '图片分组',
      'img-a',
      'img-b',
      '视频分组',
      'vid-a',
      '未分类',
      'plain'
    ])
    expect(options.filter((o) => 'kind' in o && o.kind === 'group')).toHaveLength(3)
  })

  it('keeps section header when a child matches search', () => {
    const options = withGroupSectionHeaders(items, '未分类')
    const filtered = filterSectionedGroupOptions(options, 'img-b')
    expect(filtered.map((o) => o.label)).toEqual(['图片分组', 'img-b'])
  })

  it('matches section name and keeps all children in that section', () => {
    const options = withGroupSectionHeaders(items, '未分类')
    const filtered = filterSectionedGroupOptions(options, '视频')
    expect(filtered.map((o) => o.label)).toEqual(['视频分组', 'vid-a'])
  })

  it('does not allow selecting headers (disabled)', () => {
    const options = withGroupSectionHeaders(items, '未分类')
    const headers = options.filter((o) => 'kind' in o && o.kind === 'group')
    expect(headers.every((h) => h.disabled === true)).toBe(true)
  })
})
