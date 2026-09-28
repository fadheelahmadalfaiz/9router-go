// Pure helpers for the providers overview "Test All" flows. Kept out of the
// component so the grouping, summary text and result ordering are testable
// without a render harness.
import type { BatchTestMode, BatchTestResponse, BatchTestResult } from '../../api/client'

export const BATCH_MODES: BatchTestMode[] = ['oauth', 'free', 'apikey', 'provider', 'all']

export interface BatchTestLabels {
  testing: string
  empty: string
  allPassed: string
  someFailed: string
  error: string
}

export const DEFAULT_BATCH_LABELS: BatchTestLabels = {
  testing: 'Testing…',
  empty: 'No connections to test',
  allPassed: 'All {total} tests passed',
  someFailed: '{passed}/{total} passed, {failed} failed',
  error: 'Test request failed',
}

export function fillTemplate(template: string, values: Record<string, string | number>): string {
  return template.replace(/\{(\w+)\}/g, (match, key: string) =>
    key in values ? String(values[key]) : match,
  )
}

// A run is only worth reporting when it actually probed something: an empty
// group is the normal state of a fresh install, not a pass.
export function formatBatchSummary(
  summary: BatchTestResponse['summary'],
  labels: BatchTestLabels = DEFAULT_BATCH_LABELS,
): string {
  if (summary.total === 0) return labels.empty
  if (summary.failed === 0) {
    return fillTemplate(labels.allPassed, { total: summary.total, passed: summary.passed })
  }
  return fillTemplate(labels.someFailed, {
    total: summary.total,
    passed: summary.passed,
    failed: summary.failed,
  })
}

export function isBatchSummarySuccess(summary: BatchTestResponse['summary']): boolean {
  return summary.total > 0 && summary.failed === 0
}

// Failures first so a long list does not bury the reason the user clicked.
// Ties keep the server's order, which is the order the accounts are stored in.
export function sortBatchResults(results: BatchTestResult[]): BatchTestResult[] {
  return [...results].sort((a, b) => Number(a.valid) - Number(b.valid))
}

export function getBatchFailures(results: BatchTestResult[]): BatchTestResult[] {
  return results.filter((result) => !result.valid)
}

// "1.2s" reads better than "1204ms" for the latency shown next to a name.
export function formatLatency(latencyMs: number): string {
  if (!Number.isFinite(latencyMs) || latencyMs < 0) return '-'
  if (latencyMs < 1000) return `${Math.round(latencyMs)}ms`
  return `${(latencyMs / 1000).toFixed(1)}s`
}

// A fresh response is the only safe default: a leftover summary from the
// previous provider would be reported against the new one.
export function emptyBatchResponse(mode: BatchTestMode): BatchTestResponse {
  return {
    mode,
    providerId: null,
    results: [],
    summary: { total: 0, passed: 0, failed: 0 },
    testedAt: '',
  }
}
