// Row parsing for /api/usage/request-logs. The endpoint returns pipe-delimited
// strings (upstream src/lib/db/repos/usageRepo.js getRecentLogs), so a row with
// fewer than the expected fields is dropped rather than rendered with shifted
// columns — the same guard the upstream log table applies.
export const REQUEST_LOG_FIELD_COUNT = 7

export interface RequestLogRow {
  date: string
  model: string
  provider: string
  account: string
  tokensIn: string
  tokensOut: string
  status: string
}

export function parseRequestLog(line: string): RequestLogRow | null {
  const parts = line.split(' | ')
  if (parts.length < REQUEST_LOG_FIELD_COUNT) return null
  return {
    date: parts[0],
    model: parts[1],
    provider: parts[2],
    account: parts[3],
    tokensIn: parts[4],
    tokensOut: parts[5],
    status: parts[6],
  }
}

export function parseRequestLogs(lines: string[] | undefined | null): RequestLogRow[] {
  if (!Array.isArray(lines)) return []
  const rows: RequestLogRow[] = []
  for (const line of lines) {
    const row = parseRequestLog(line)
    if (row) rows.push(row)
  }
  return rows
}

export type RequestLogStatus = 'ok' | 'failed' | 'pending'

export function getRequestLogStatus(status: string): RequestLogStatus {
  if (status.includes('FAILED')) return 'failed'
  if (status.includes('PENDING')) return 'pending'
  return 'ok'
}
