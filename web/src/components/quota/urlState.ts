// Query-string state for /dashboard/quota so filters, sort and pagination
// survive a refresh, can be shared as a link, and are restored by back/forward.
// Pure helpers only: the component owns the history side effects.
import {
  ACCOUNT_FILTER_OPTIONS,
  ACCOUNT_PAGE_SIZE_MAX,
  DEFAULT_ACCOUNT_PAGE_SIZE,
  QUOTA_SORT_OPTIONS,
} from './types'

export interface QuotaUrlState {
  provider: string
  account: string
  sort: string
  expiring: boolean
  page: number
  pageSize: number
}

export const QUOTA_URL_DEFAULTS: QuotaUrlState = {
  provider: 'all',
  account: 'all',
  sort: 'default',
  expiring: false,
  page: 1,
  pageSize: DEFAULT_ACCOUNT_PAGE_SIZE,
}

export const QUOTA_URL_PARAMS = {
  provider: 'provider',
  account: 'account',
  sort: 'sort',
  expiring: 'expiring',
  page: 'page',
  pageSize: 'pageSize',
} as const

// Provider ids are forwarded to the dashboard API, so only allow the
// character set real provider ids use.
const PROVIDER_PATTERN = /^[a-z0-9][a-z0-9._-]{0,63}$/i

const ACCOUNT_VALUES = new Set(ACCOUNT_FILTER_OPTIONS.map((option) => option.value))
const SORT_VALUES = new Set(QUOTA_SORT_OPTIONS.map((option) => option.value))
const KNOWN_PARAMS = new Set<string>(Object.values(QUOTA_URL_PARAMS))

function parseProvider(raw: string | null): string {
  const value = (raw || '').trim().toLowerCase()
  if (!value || value === 'all' || !PROVIDER_PATTERN.test(value)) {
    return QUOTA_URL_DEFAULTS.provider
  }
  return value
}

function parseEnum(raw: string | null, allowed: Set<string>, fallback: string): string {
  const value = (raw || '').trim()
  return value && allowed.has(value) ? value : fallback
}

function parseBoolean(raw: string | null): boolean {
  return (raw || '').trim() === '1' || (raw || '').trim().toLowerCase() === 'true'
}

function parsePositiveInt(raw: string | null, fallback: number): number {
  const parsed = Number.parseInt((raw || '').trim(), 10)
  return Number.isFinite(parsed) && parsed >= 1 ? parsed : fallback
}

function parsePageSize(raw: string | null): number {
  const parsed = Number.parseInt((raw || '').trim(), 10)
  if (!Number.isFinite(parsed) || parsed < 1) return QUOTA_URL_DEFAULTS.pageSize
  return Math.min(parsed, ACCOUNT_PAGE_SIZE_MAX)
}

// Always parses against the defaults, never against the current state: going
// back to a URL without a param must clear that filter, not keep it.
export function parseQuotaUrlState(search: string): QuotaUrlState {
  const params = new URLSearchParams(search || '')
  return {
    provider: parseProvider(params.get(QUOTA_URL_PARAMS.provider)),
    account: parseEnum(params.get(QUOTA_URL_PARAMS.account), ACCOUNT_VALUES, QUOTA_URL_DEFAULTS.account),
    sort: parseEnum(params.get(QUOTA_URL_PARAMS.sort), SORT_VALUES, QUOTA_URL_DEFAULTS.sort),
    expiring: params.has(QUOTA_URL_PARAMS.expiring)
      ? parseBoolean(params.get(QUOTA_URL_PARAMS.expiring))
      : QUOTA_URL_DEFAULTS.expiring,
    page: parsePositiveInt(params.get(QUOTA_URL_PARAMS.page), QUOTA_URL_DEFAULTS.page),
    pageSize: parsePageSize(params.get(QUOTA_URL_PARAMS.pageSize)),
  }
}

function buildQuotaParams(state: QuotaUrlState): URLSearchParams {
  const params = new URLSearchParams()
  if (state.provider !== QUOTA_URL_DEFAULTS.provider) params.set(QUOTA_URL_PARAMS.provider, state.provider)
  if (state.account !== QUOTA_URL_DEFAULTS.account) params.set(QUOTA_URL_PARAMS.account, state.account)
  if (state.sort !== QUOTA_URL_DEFAULTS.sort) params.set(QUOTA_URL_PARAMS.sort, state.sort)
  if (state.expiring) params.set(QUOTA_URL_PARAMS.expiring, '1')
  if (state.page > QUOTA_URL_DEFAULTS.page) params.set(QUOTA_URL_PARAMS.page, String(state.page))
  if (state.pageSize !== QUOTA_URL_DEFAULTS.pageSize) {
    params.set(QUOTA_URL_PARAMS.pageSize, String(state.pageSize))
  }
  return params
}

// Returns a search string prefixed with "?" (or "" when no params). Params this
// module does not own are preserved so unrelated query state is never dropped.
export function buildQuotaUrl(state: QuotaUrlState, currentSearch = ''): string {
  const params = new URLSearchParams(currentSearch || '')
  for (const key of KNOWN_PARAMS) params.delete(key)
  for (const [key, value] of buildQuotaParams(state)) params.set(key, value)
  const query = params.toString()
  return query ? `?${query}` : ''
}

// Filter changes push a history entry so back/forward undoes them; page-only
// changes must not, or paging floods the history stack.
export function getQuotaFilterSignature(state: QuotaUrlState): string {
  return [
    state.provider,
    state.account,
    state.sort,
    state.expiring ? '1' : '0',
    state.pageSize,
  ].join('|')
}
