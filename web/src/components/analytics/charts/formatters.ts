// Formatting shared by the three usage charts, mirroring the upstream
// recharts formatters so a ported chart shows the same numbers.
export function fmtTokens(n?: number): string {
  const value = n || 0
  if (value >= 1000000) return `${(value / 1000000).toFixed(1)}M`
  if (value >= 1000) return `${(value / 1000).toFixed(1)}K`
  return String(value)
}

export function fmtCost(n?: number): string {
  return `$${(n || 0).toFixed(4)}`
}

export function fmtRequests(n?: number): string {
  return String(n || 0)
}

export function truncateLabel(value: string, max: number): string {
  if (!value) return ''
  return value.length > max ? `${value.slice(0, max)}…` : value
}
