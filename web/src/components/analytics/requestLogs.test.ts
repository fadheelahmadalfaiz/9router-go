import { describe, expect, it } from 'bun:test'
import { getRequestLogStatus, parseRequestLog, parseRequestLogs } from './requestLogs'

const LINE = '28-09-2026 10:00:00 | gpt-5 | CODEX | Work | 1200 | 340 | OK'

describe('parseRequestLog', () => {
  it('splits the pipe-delimited row into fields', () => {
    expect(parseRequestLog(LINE)).toEqual({
      date: '28-09-2026 10:00:00',
      model: 'gpt-5',
      provider: 'CODEX',
      account: 'Work',
      tokensIn: '1200',
      tokensOut: '340',
      status: 'OK',
    })
  })

  it('drops a row that is missing fields rather than shifting columns', () => {
    expect(parseRequestLog('28-09-2026 10:00:00 | gpt-5 | CODEX')).toBeNull()
    expect(parseRequestLog('')).toBeNull()
  })

  it('keeps surplus fields out of the mapped shape', () => {
    const row = parseRequestLog(`${LINE} | EXTRA | MORE`)
    expect(row).not.toBeNull()
    expect(row!.status).toBe('OK')
  })
})

describe('parseRequestLogs', () => {
  it('keeps only the parseable rows', () => {
    const rows = parseRequestLogs([LINE, 'broken row', LINE])
    expect(rows).toHaveLength(2)
  })

  it('returns an empty list for a non-array payload', () => {
    expect(parseRequestLogs(undefined)).toEqual([])
    expect(parseRequestLogs(null)).toEqual([])
    expect(parseRequestLogs([])).toEqual([])
  })
})

describe('getRequestLogStatus', () => {
  it('classifies the three upstream states', () => {
    expect(getRequestLogStatus('OK')).toBe('ok')
    expect(getRequestLogStatus('FAILED')).toBe('failed')
    expect(getRequestLogStatus('PENDING')).toBe('pending')
  })

  it('matches a compound status containing the marker', () => {
    expect(getRequestLogStatus('FAILED:429')).toBe('failed')
    expect(getRequestLogStatus('PENDING_STREAM')).toBe('pending')
  })

  it('falls back to ok for an unknown status', () => {
    expect(getRequestLogStatus('WEIRD')).toBe('ok')
  })
})
