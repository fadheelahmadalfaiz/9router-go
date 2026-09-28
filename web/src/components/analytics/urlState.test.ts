import { describe, expect, it } from 'bun:test'
import { USAGE_URL_DEFAULTS, buildUsageUrl, parseUsageUrlState, type UsageUrlState } from './urlState'

const base: UsageUrlState = { ...USAGE_URL_DEFAULTS }

describe('parseUsageUrlState', () => {
  const cases: { name: string; search: string; expected: UsageUrlState }[] = [
    { name: 'empty search falls back to defaults', search: '', expected: base },
    {
      name: 'parses every known param',
      search: '?tab=details&period=30d&table=endpoint&view=tokens&sortBy=cost&sortOrder=desc',
      expected: {
        tab: 'details',
        period: '30d',
        table: 'endpoint',
        view: 'tokens',
        sortBy: 'cost',
        sortOrder: 'desc',
      },
    },
    { name: 'keeps the logs tab', search: '?tab=logs', expected: { ...base, tab: 'logs' } },
    { name: 'keeps the all period', search: '?period=all', expected: { ...base, period: 'all' } },
    { name: 'rejects an unknown tab', search: '?tab=nope', expected: base },
    { name: 'rejects an unknown period', search: '?period=90d', expected: base },
    { name: 'rejects an unknown table view', search: '?table=combo', expected: base },
    { name: 'rejects an unknown view mode', search: '?view=raw', expected: base },
    { name: 'rejects an unsortable field', search: '?sortBy=__proto__', expected: base },
    { name: 'rejects a non-column sort field', search: '?sortBy=timestamp', expected: base },
    { name: 'rejects an unknown sort order', search: '?sortOrder=sideways', expected: base },
    {
      name: 'accepts every documented sortable field',
      search: '?sortBy=cachedCost&sortOrder=desc',
      expected: { ...base, sortBy: 'cachedCost', sortOrder: 'desc' },
    },
    { name: 'ignores unrelated params', search: '?ref=email&tab=logs', expected: { ...base, tab: 'logs' } },
  ]

  for (const testCase of cases) {
    it(testCase.name, () => {
      expect(parseUsageUrlState(testCase.search)).toEqual(testCase.expected)
    })
  }
})

describe('buildUsageUrl', () => {
  const cases: { name: string; state: UsageUrlState; currentSearch: string; expected: string }[] = [
    { name: 'default state produces no query string', state: base, currentSearch: '', expected: '' },
    {
      name: 'omits every default param',
      state: base,
      currentSearch: '?tab=details&period=7d',
      expected: '',
    },
    {
      name: 'writes a single non-default param',
      state: { ...base, tab: 'logs' },
      currentSearch: '',
      expected: '?tab=logs',
    },
    {
      name: 'writes the full non-default set',
      state: { tab: 'details', period: 'all', table: 'apiKey', view: 'tokens', sortBy: 'cost', sortOrder: 'desc' },
      currentSearch: '',
      expected: '?tab=details&period=all&table=apiKey&view=tokens&sortBy=cost&sortOrder=desc',
    },
    {
      name: 'preserves params it does not own',
      state: { ...base, period: '7d' },
      currentSearch: '?ref=email',
      expected: '?ref=email&period=7d',
    },
  ]

  for (const testCase of cases) {
    it(testCase.name, () => {
      expect(buildUsageUrl(testCase.state, testCase.currentSearch)).toBe(testCase.expected)
    })
  }

  it('is idempotent for an already canonical url', () => {
    const state = parseUsageUrlState('?period=60d&sortOrder=desc')
    const once = buildUsageUrl(state, '')
    expect(buildUsageUrl(state, once)).toBe(once)
  })

  it('round-trips every non-default value', () => {
    const state: UsageUrlState = {
      tab: 'logs',
      period: 'all',
      table: 'account',
      view: 'tokens',
      sortBy: 'totalTokens',
      sortOrder: 'desc',
    }
    expect(parseUsageUrlState(buildUsageUrl(state, ''))).toEqual(state)
  })
})
