export function formatLocalDateInput(date: Date): string {
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
}

export function defaultAsyncImageTaskDateFilters(now = new Date()): { start_date: string; end_date: string } {
  const today = formatLocalDateInput(now)
  return { start_date: today, end_date: today }
}
