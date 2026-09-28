import { describe, expect, it } from 'bun:test'
import { ACCOUNT_PAGE_SIZE_MAX, DEFAULT_ACCOUNT_PAGE_SIZE } from './types'
import {
  QUOTA_URL_DEFAULTS,
  buildQuotaUrl,
  getQuotaFilterSignature,
  parseQuotaUrlState,
  type QuotaUrlState,
} from './urlState'

const base: QuotaUrlState = { ...QUOTA_URL_DEFAULTS }

describe('parseQuotaUrlState', () => {
  const cases: { name: string; search: string; expected: QuotaUrlState }[] = [
    {
      name: 'empty search falls back to defaults',
      search: '',
      expected: base,
    },
    {
      name: 'parses every known param',
      search: '?provider=claude&account=active&sort=remaining-asc&expiring=1&page=3&pageSize=50',
      expected: {
        provider: 'claude',
        account: 'active',
        sort: 'remaining-asc',
        expiring: true,
        page: 3,
        pageSize: 50,
      },
    },
    {
      name: 'normalizes provider casing and whitespace',
      search: '?provider=%20Claude%20',
      expected: { ...base, provider: 'claude' },
    },
    {
      name: 'provider "all" collapses to the default',
      search: '?provider=all',
      expected: base,
    },
    {
      name: 'rejects a provider with unsafe characters',
      search: '?provider=../../etc&sort=default',
      expected: base,
    },
    {
      name: 'rejects an unknown account status',
      search: '?account=bogus',
      expected: base,
    },
    {
      name: 'rejects an unknown sort mode',
      search: '?sort=bogus',
      expected: base,
    },
    {
      name: 'expiring is false when present but falsy',
      search: '?expiring=0',
      expected: base,
    },
    {
      name: 'expiring accepts true as well as 1',
      search: '?expiring=true',
      expected: { ...base, expiring: true },
    },
    {
      name: 'rejects zero and negative pages',
      search: '?page=0',
      expected: base,
    },
    {
      name: 'rejects a non-numeric page',
      search: '?page=abc',
      expected: base,
    },
    {
      name: 'clamps page size to the maximum',
      search: `?pageSize=${ACCOUNT_PAGE_SIZE_MAX + 1}`,
      expected: { ...base, pageSize: ACCOUNT_PAGE_SIZE_MAX },
    },
    {
      name: 'keeps a custom page size below the presets',
      search: '?pageSize=7',
      expected: { ...base, pageSize: 7 },
    },
    {
      name: 'ignores unrelated params',
      search: '?foo=bar&provider=kiro',
      expected: { ...base, provider: 'kiro' },
    },
  ]

  for (const testCase of cases) {
    it(testCase.name, () => {
      expect(parseQuotaUrlState(testCase.search)).toEqual(testCase.expected)
    })
  }

  it('always resolves to a valid page size', () => {
    const parsed = parseQuotaUrlState('?pageSize=0')
    expect(parsed.pageSize).toBe(DEFAULT_ACCOUNT_PAGE_SIZE)
  })
})

describe('buildQuotaUrl', () => {
  const cases: { name: string; state: QuotaUrlState; currentSearch: string; expected: string }[] = [
    { name: 'default state produces no query string', state: base, currentSearch: '', expected: '' },
    {
      name: 'omits every default param',
      state: base,
      currentSearch: '?provider=claude&account=active&page=4',
      expected: '',
    },
    {
      name: 'writes non-default filters',
      state: { ...base, provider: 'claude', account: 'inactive' },
      currentSearch: '',
      expected: '?provider=claude&account=inactive',
    },
    {
      name: 'writes expiring, page and page size',
      state: { ...base, expiring: true, page: 2, pageSize: 50 },
      currentSearch: '',
      expected: '?expiring=1&page=2&pageSize=50',
    },
    {
      name: 'preserves params it does not own',
      state: { ...base, provider: 'kiro' },
      currentSearch: '?ref=email',
      expected: '?ref=email&provider=kiro',
    },
    {
      name: 'round-trips a parsed url',
      state: parseQuotaUrlState('?provider=codex&account=active&sort=remaining-desc&expiring=1&page=7&pageSize=100'),
      currentSearch: '?provider=codex&account=active&sort=remaining-desc&expiring=1&page=7&pageSize=100',
      expected: '?provider=codex&account=active&sort=remaining-desc&expiring=1&page=7&pageSize=100',
    },
  ]

  for (const testCase of cases) {
    it(testCase.name, () => {
      expect(buildQuotaUrl(testCase.state, testCase.currentSearch)).toBe(testCase.expected)
    })
  }

  it('is idempotent for an already canonical url', () => {
    const state = parseQuotaUrlState('?provider=claude&page=2')
    const once = buildQuotaUrl(state, '')
    expect(buildQuotaUrl(state, once)).toBe(once)
  })
})

describe('getQuotaFilterSignature', () => {
  it('ignores the page so paging does not push history', () => {
    const first = { ...base, provider: 'claude', page: 1 }
    const later = { ...base, provider: 'claude', page: 9 }
    expect(getQuotaFilterSignature(first)).toBe(getQuotaFilterSignature(later))
  })

  it('changes for each filter dimension', () => {
    const signature = getQuotaFilterSignature(base)
    expect(getQuotaFilterSignature({ ...base, provider: 'claude' })).not.toBe(signature)
    expect(getQuotaFilterSignature({ ...base, account: 'active' })).not.toBe(signature)
    expect(getQuotaFilterSignature({ ...base, sort: 'remaining-asc' })).not.toBe(signature)
    expect(getQuotaFilterSignature({ ...base, expiring: true })).not.toBe(signature)
    expect(getQuotaFilterSignature({ ...base, pageSize: 50 })).not.toBe(signature)
  })
})
