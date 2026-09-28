// Query-string state for /dashboard/usage so the tab, period, table view and
// sort order survive a refresh, can be shared as a link, and are restored by
// back/forward. Pure helpers only: the component owns the history side effects.
import { PERIODS, TABLE_OPTIONS, type MainTab, type Period, type SortOrder, type TableView, type ViewMode } from './types'

export interface UsageUrlState {
  tab: MainTab
  period: Period
  table: TableView
  view: ViewMode
  sortBy: string
  sortOrder: SortOrder
}

export const USAGE_URL_DEFAULTS: UsageUrlState = {
  tab: 'overview',
  period: 'today',
  table: 'model',
  view: 'costs',
  sortBy: 'rawModel',
  sortOrder: 'asc',
}

export const USAGE_URL_PARAMS = {
  tab: 'tab',
  period: 'period',
  table: 'table',
  view: 'view',
  sortBy: 'sortBy',
  sortOrder: 'sortOrder',
} as const

const TABS: MainTab[] = ['overview', 'details', 'logs']
const VIEWS: ViewMode[] = ['costs', 'tokens']

// Sortable fields, mirroring the union of every table's column set. Anything
// else in the URL is dropped so a hand-edited link cannot drive the comparator
// with an unknown key.
const SORTABLE_FIELDS = new Set([
  'rawModel',
  'provider',
  'requests',
  'lastUsed',
  'accountName',
  'keyName',
  'endpoint',
  'promptTokens',
  'cachedTokens',
  'completionTokens',
  'totalTokens',
  'inputCost',
  'cachedCost',
  'outputCost',
  'cost',
])

function pick<T extends string>(raw: string | null, allowed: readonly T[], fallback: T): T {
  const value = (raw || '').trim()
  return value && (allowed as readonly string[]).includes(value) ? (value as T) : fallback
}

function pickEnum<T extends string>(raw: string | null, options: { value: T }[], fallback: T): T {
  const value = (raw || '').trim()
  return options.some((o) => o.value === value) ? (value as T) : fallback
}

function parseSortBy(raw: string | null): string {
  const value = (raw || '').trim()
  return SORTABLE_FIELDS.has(value) ? value : USAGE_URL_DEFAULTS.sortBy
}

function parseSortOrder(raw: string | null): SortOrder {
  return (raw || '').trim() === 'desc' ? 'desc' : USAGE_URL_DEFAULTS.sortOrder
}

// Always parses against the defaults, never against the current state: going
// back to a URL without a param must clear that filter, not keep it.
export function parseUsageUrlState(search: string): UsageUrlState {
  const params = new URLSearchParams(search || '')
  return {
    tab: pick(params.get(USAGE_URL_PARAMS.tab), TABS, USAGE_URL_DEFAULTS.tab),
    period: pickEnum(params.get(USAGE_URL_PARAMS.period), PERIODS, USAGE_URL_DEFAULTS.period),
    table: pickEnum(params.get(USAGE_URL_PARAMS.table), TABLE_OPTIONS, USAGE_URL_DEFAULTS.table),
    view: pick(params.get(USAGE_URL_PARAMS.view), VIEWS, USAGE_URL_DEFAULTS.view),
    sortBy: parseSortBy(params.get(USAGE_URL_PARAMS.sortBy)),
    sortOrder: parseSortOrder(params.get(USAGE_URL_PARAMS.sortOrder)),
  }
}

function buildUsageParams(state: UsageUrlState): URLSearchParams {
  const params = new URLSearchParams()
  if (state.tab !== USAGE_URL_DEFAULTS.tab) params.set(USAGE_URL_PARAMS.tab, state.tab)
  if (state.period !== USAGE_URL_DEFAULTS.period) params.set(USAGE_URL_PARAMS.period, state.period)
  if (state.table !== USAGE_URL_DEFAULTS.table) params.set(USAGE_URL_PARAMS.table, state.table)
  if (state.view !== USAGE_URL_DEFAULTS.view) params.set(USAGE_URL_PARAMS.view, state.view)
  if (state.sortBy !== USAGE_URL_DEFAULTS.sortBy) params.set(USAGE_URL_PARAMS.sortBy, state.sortBy)
  if (state.sortOrder !== USAGE_URL_DEFAULTS.sortOrder) {
    params.set(USAGE_URL_PARAMS.sortOrder, state.sortOrder)
  }
  return params
}

// Returns a search string prefixed with "?" (or "" when no params). Params this
// module does not own are preserved so unrelated query state is never dropped.
export function buildUsageUrl(state: UsageUrlState, currentSearch = ''): string {
  const params = new URLSearchParams(currentSearch || '')
  for (const key of Object.values(USAGE_URL_PARAMS)) params.delete(key)
  for (const [key, value] of buildUsageParams(state)) params.set(key, value)
  const query = params.toString()
  return query ? `?${query}` : ''
}
