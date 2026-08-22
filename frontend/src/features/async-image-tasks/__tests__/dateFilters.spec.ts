import { describe, expect, it } from 'vitest'

import { defaultAsyncImageTaskDateFilters, formatLocalDateInput } from '../dateFilters'

describe('async image task date filters', () => {
  it('uses the browser-local calendar date for both default boundaries', () => {
    const localDate = new Date(2026, 7, 22, 23, 59, 0)

    expect(formatLocalDateInput(localDate)).toBe('2026-08-22')
    expect(defaultAsyncImageTaskDateFilters(localDate)).toEqual({
      start_date: '2026-08-22',
      end_date: '2026-08-22',
    })
  })
})
