/** Normalize group section label (trim; empty means uncategorized). */
export function normalizeGroupSection(section?: string | null): string {
  return (section ?? '').trim()
}

export interface SectionHeaderOption {
  value: number
  label: string
  kind: 'group'
  disabled: true
}

export type SectionedOption<T extends { value: number }> = T | SectionHeaderOption

/**
 * Insert disabled section headers before each section of options.
 * Input order is preserved (backend already sorts empty section last).
 * Header values use negative sentinels so they never collide with real group ids.
 */
export function withGroupSectionHeaders<
  T extends { value: number; section?: string | null }
>(items: T[], uncategorizedLabel: string): SectionedOption<T>[] {
  if (items.length === 0) return []

  const sections = new Map<string, T[]>()
  const order: string[] = []

  for (const item of items) {
    const key = normalizeGroupSection(item.section)
    if (!sections.has(key)) {
      sections.set(key, [])
      order.push(key)
    }
    sections.get(key)!.push(item)
  }

  const options: SectionedOption<T>[] = []
  let headerIdx = 0
  for (const key of order) {
    const groupItems = sections.get(key)!
    options.push({
      value: -(headerIdx + 1),
      label: key || uncategorizedLabel,
      kind: 'group',
      disabled: true
    })
    headerIdx += 1
    options.push(...groupItems)
  }
  return options
}

function isSectionHeader(opt: { kind?: string }): boolean {
  return opt.kind === 'group'
}

/**
 * Filter sectioned options by name / description / section.
 * Keeps a section header when any item in that section matches,
 * or when the header label itself matches (then all items in the section are kept).
 */
export function filterSectionedGroupOptions<
  T extends {
    value: number
    label: string
    description?: string | null
    section?: string | null
    kind?: string
  }
>(options: T[], query: string): T[] {
  const q = query.trim().toLowerCase()
  if (!q) return options

  const result: T[] = []
  let i = 0
  while (i < options.length) {
    const opt = options[i]
    if (isSectionHeader(opt)) {
      const header = opt
      const headerMatches = header.label.toLowerCase().includes(q)
      const children: T[] = []
      i += 1
      while (i < options.length && !isSectionHeader(options[i])) {
        children.push(options[i])
        i += 1
      }
      if (headerMatches) {
        result.push(header, ...children)
        continue
      }
      const matchedChildren = children.filter((child) => {
        if (child.label.toLowerCase().includes(q)) return true
        if (child.description && child.description.toLowerCase().includes(q)) return true
        const section = normalizeGroupSection(child.section)
        if (section && section.toLowerCase().includes(q)) return true
        return false
      })
      if (matchedChildren.length > 0) {
        result.push(header, ...matchedChildren)
      }
      continue
    }

    if (
      opt.label.toLowerCase().includes(q) ||
      (opt.description && opt.description.toLowerCase().includes(q)) ||
      (normalizeGroupSection(opt.section) &&
        normalizeGroupSection(opt.section).toLowerCase().includes(q))
    ) {
      result.push(opt)
    }
    i += 1
  }
  return result
}
