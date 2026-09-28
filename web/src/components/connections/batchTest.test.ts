import { describe, expect, it } from 'bun:test'
import type { BatchTestResult, BatchTestSummary } from '../../api/client'
import {
  DEFAULT_BATCH_LABELS,
  emptyBatchResponse,
  fillTemplate,
  formatBatchSummary,
  formatLatency,
  getBatchFailures,
  isBatchSummarySuccess,
  sortBatchResults,
} from './batchTest'

function summary(partial: Partial<BatchTestSummary>): BatchTestSummary {
  return { total: 0, passed: 0, failed: 0, ...partial }
}

function result(overrides: Partial<BatchTestResult> = {}): BatchTestResult {
  return {
    provider: 'claude',
    connectionId: 'conn-1',
    connectionName: 'Work',
    authType: 'oauth',
    authGroup: 'oauth',
    valid: true,
    latencyMs: 120,
    refreshed: false,
    error: null,
    testedAt: '2026-09-28T10:00:00Z',
    ...overrides,
  }
}

describe('formatBatchSummary', () => {
  it('reports an empty group as nothing to do, not as a pass', () => {
    expect(formatBatchSummary(summary({}))).toBe(DEFAULT_BATCH_LABELS.empty)
  })

  it('reports a clean run with the count', () => {
    expect(formatBatchSummary(summary({ total: 4, passed: 4 }))).toBe('All 4 tests passed')
  })

  it('reports a mixed run with passed and failed counts', () => {
    expect(formatBatchSummary(summary({ total: 7, passed: 5, failed: 2 }))).toBe('5/7 passed, 2 failed')
  })

  it('honours custom labels', () => {
    const custom = { ...DEFAULT_BATCH_LABELS, allPassed: '{total} ok' }
    expect(formatBatchSummary(summary({ total: 2, passed: 2 }), custom)).toBe('2 ok')
  })
})

describe('isBatchSummarySuccess', () => {
  it('is false for an empty run', () => {
    expect(isBatchSummarySuccess(summary({}))).toBe(false)
  })

  it('is true only when something passed and nothing failed', () => {
    expect(isBatchSummarySuccess(summary({ total: 1, passed: 1 }))).toBe(true)
    expect(isBatchSummarySuccess(summary({ total: 2, passed: 1, failed: 1 }))).toBe(false)
  })
})

describe('sortBatchResults', () => {
  it('puts failures first', () => {
    const sorted = sortBatchResults([result(), result({ valid: false }), result()])
    expect(sorted[0].valid).toBe(false)
  })

  it('does not mutate the input', () => {
    const input = [result(), result({ valid: false })]
    sortBatchResults(input)
    expect(input[0].valid).toBe(true)
  })

  it('keeps a stable order among rows with the same outcome', () => {
    const first = result({ connectionId: 'a' })
    const second = result({ connectionId: 'b' })
    expect(sortBatchResults([first, second]).map((r) => r.connectionId)).toEqual(['a', 'b'])
  })
})

describe('getBatchFailures', () => {
  it('returns only the failed rows', () => {
    const failures = getBatchFailures([result(), result({ valid: false, error: '401' })])
    expect(failures).toHaveLength(1)
    expect(failures[0].error).toBe('401')
  })
})

describe('formatLatency', () => {
  it('renders sub-second probes in milliseconds', () => {
    expect(formatLatency(120)).toBe('120ms')
    expect(formatLatency(999)).toBe('999ms')
  })

  it('renders slower probes in seconds', () => {
    expect(formatLatency(1204)).toBe('1.2s')
  })

  it('handles a missing or nonsensical measurement', () => {
    expect(formatLatency(Number.NaN)).toBe('-')
    expect(formatLatency(-5)).toBe('-')
  })
})

describe('fillTemplate', () => {
  it('substitutes known keys', () => {
    expect(fillTemplate('{a} and {b}', { a: 1, b: 'two' })).toBe('1 and two')
  })

  it('leaves unknown placeholders alone', () => {
    expect(fillTemplate('{a} {z}', { a: 1 })).toBe('1 {z}')
  })
})

describe('emptyBatchResponse', () => {
  it('starts clean so a previous run is never reported against a new provider', () => {
    const empty = emptyBatchResponse('free')
    expect(empty.mode).toBe('free')
    expect(empty.results).toEqual([])
    expect(empty.summary).toEqual({ total: 0, passed: 0, failed: 0 })
    expect(formatBatchSummary(empty.summary)).toBe(DEFAULT_BATCH_LABELS.empty)
  })
})
