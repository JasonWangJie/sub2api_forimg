export const USER_USAGE_LATENCY_DIVISOR_DEFAULT = 1
export const USER_USAGE_LATENCY_DIVISOR_MIN = 1
export const USER_USAGE_LATENCY_DIVISOR_MAX = 1000

export const normalizeUserUsageLatencyDivisor = (value: unknown): number => {
  const divisor = Number(value)
  if (
    !Number.isFinite(divisor) ||
    divisor < USER_USAGE_LATENCY_DIVISOR_MIN ||
    divisor > USER_USAGE_LATENCY_DIVISOR_MAX
  ) {
    return USER_USAGE_LATENCY_DIVISOR_DEFAULT
  }
  return divisor
}

export const scaleUsageLatency = (
  milliseconds: number | null | undefined,
  divisor: unknown
): number | null => {
  if (milliseconds == null || !Number.isFinite(milliseconds)) return null

  const scaled = milliseconds / normalizeUserUsageLatencyDivisor(divisor)
  return Math.round((scaled + Number.EPSILON) * 100) / 100
}
