// Table model for the usage breakdown: sorting, group/expand aggregation, the
// token-share cost split, and pending-request highlighting. Ported from upstream
// src/shared/components/UsageStats.js (sortData / groupDataByKey / getGroupKey)
// and src/app/(dashboard)/dashboard/usage/components/UsageTable.js.
import type { SortOrder, StatsData, TableView, UsageItem, ViewMode } from './types'

export interface UsageRow extends UsageItem {
  key: string
  totalTokens: number
  inputCost: number
  cachedCost: number
  outputCost: number
  pending: number
}

export type SummaryRow = Omit<UsageRow, 'key' | 'rawModel' | 'accountName' | 'keyName' | 'endpoint' | 'provider'>

export interface UsageGroup {
  groupKey: string
  summary: SummaryRow
  items: UsageRow[]
}

export type SortField = keyof UsageRow | string

export const TABLE_SOURCE: Record<TableView, keyof StatsData> = {
  model: 'byModel',
  account: 'byAccount',
  apiKey: 'byApiKey',
  endpoint: 'byEndpoint',
}

export const TABLE_STORAGE_KEY: Record<TableView, string> = {
  model: 'usage-stats:expanded-models',
  account: 'usage-stats:expanded-accounts',
  apiKey: 'usage-stats:expanded-apikeys',
  endpoint: 'usage-stats:expanded-endpoints',
}

export const TABLE_EMPTY_MESSAGE: Record<TableView, string> = {
  model: 'No usage recorded yet.',
  account: 'No account-specific usage recorded yet.',
  apiKey: 'No API key usage recorded yet.',
  endpoint: 'No endpoint usage recorded yet.',
}

// Cost is a token-share allocation of the server total, not a per-rate
// recompute. Cached tokens are a subset of prompt tokens, so they are peeled out
// of the input share rather than added on top of it.
function splitCost(item: UsageItem): Pick<UsageRow, 'inputCost' | 'cachedCost' | 'outputCost'> {
  const totalTokens = (item.promptTokens || 0) + (item.completionTokens || 0)
  const totalCost = item.cost || 0
  if (totalTokens <= 0) return { inputCost: 0, cachedCost: 0, outputCost: 0 }
  const cachedTokens = item.cachedTokens || 0
  const nonCachedInput = Math.max(0, (item.promptTokens || 0) - cachedTokens)
  return {
    inputCost: nonCachedInput * (totalCost / totalTokens),
    cachedCost: cachedTokens * (totalCost / totalTokens),
    outputCost: (item.completionTokens || 0) * (totalCost / totalTokens),
  }
}

function compare(a: unknown, b: unknown, order: SortOrder): number {
  let left = a
  let right = b
  if (typeof left === 'string') left = left.toLowerCase()
  if (typeof right === 'string') right = right.toLowerCase()
  if (left === right) return 0
  if (left === undefined || left === null) return order === 'asc' ? 1 : -1
  if (right === undefined || right === null) return order === 'asc' ? -1 : 1
  if (left < right) return order === 'asc' ? -1 : 1
  return order === 'asc' ? 1 : -1
}

export function getGroupKey(item: UsageItem, tableView: TableView): string {
  switch (tableView) {
    case 'model':
      return item.rawModel || 'Unknown Model'
    case 'account':
      return item.accountName || (item.key ? item.key : 'Unknown Account')
    case 'apiKey':
      return item.keyName || 'Unknown Key'
    case 'endpoint':
      return item.endpoint || 'Unknown Endpoint'
    default:
      return String(item.key || 'Unknown')
  }
}

// Upstream keys pending per model for the model view, but per account for the
// account view: pending.byAccount[connectionId]["<rawModel> (<provider>)"].
// The Go backend fills byAccount keys as "<rawModel> (<provider> - <account>)",
// so the composite key has to be rebuilt from the row rather than reused.
function pendingFor(
  row: UsageItem & { key: string },
  tableView: TableView,
  byModel: Record<string, number>,
  byAccount: Record<string, Record<string, number>>,
): number {
  if (tableView === 'model') return byModel[row.key] || 0
  if (tableView !== 'account') return 0
  const connectionId = row.connectionId
  if (!connectionId) return 0
  const perConnection = byAccount[connectionId]
  if (!perConnection) return 0
  const modelKey = row.provider ? `${row.rawModel} (${row.provider})` : row.rawModel || ''
  return perConnection[modelKey] || 0
}

export function buildRows(
  stats: StatsData | undefined,
  tableView: TableView,
  sortBy: string,
  sortOrder: SortOrder,
): UsageRow[] {
  const source = (stats?.[TABLE_SOURCE[tableView]] || {}) as Record<string, UsageItem>
  const byModel = stats?.pending?.byModel || {}
  const byAccount = stats?.pending?.byAccount || {}

  const rows: UsageRow[] = Object.entries(source).map(([key, item]) => {
    const promptTokens = item.promptTokens || 0
    const completionTokens = item.completionTokens || 0
    return {
      ...item,
      key,
      promptTokens,
      completionTokens,
      totalTokens: promptTokens + completionTokens,
      ...splitCost(item),
      pending: pendingFor({ ...item, key }, tableView, byModel, byAccount),
    }
  })

  rows.sort((a, b) => compare(a[sortBy as keyof UsageRow], b[sortBy as keyof UsageRow], sortOrder))
  return rows
}

function emptySummary(): SummaryRow {
  return {
    requests: 0,
    promptTokens: 0,
    completionTokens: 0,
    cachedTokens: 0,
    totalTokens: 0,
    cost: 0,
    inputCost: 0,
    cachedCost: 0,
    outputCost: 0,
    lastUsed: '',
    pending: 0,
  }
}

export function groupRows(rows: UsageRow[], tableView: TableView): UsageGroup[] {
  const groups = new Map<string, UsageGroup>()
  for (const row of rows) {
    const groupKey = getGroupKey(row, tableView)
    let group = groups.get(groupKey)
    if (!group) {
      group = { groupKey, summary: emptySummary(), items: [] }
      groups.set(groupKey, group)
    }
    const summary = group.summary
    summary.requests = (summary.requests || 0) + (row.requests || 0)
    summary.promptTokens = (summary.promptTokens || 0) + (row.promptTokens || 0)
    summary.completionTokens = (summary.completionTokens || 0) + (row.completionTokens || 0)
    summary.cachedTokens = (summary.cachedTokens || 0) + (row.cachedTokens || 0)
    summary.totalTokens = (summary.totalTokens || 0) + row.totalTokens
    summary.cost = (summary.cost || 0) + (row.cost || 0)
    summary.inputCost = (summary.inputCost || 0) + row.inputCost
    summary.cachedCost = (summary.cachedCost || 0) + row.cachedCost
    summary.outputCost = (summary.outputCost || 0) + row.outputCost
    summary.pending = (summary.pending || 0) + row.pending
    if (row.lastUsed && (!summary.lastUsed || row.lastUsed > summary.lastUsed)) {
      summary.lastUsed = row.lastUsed
    }
    group.items.push(row)
  }
  return [...groups.values()]
}

export function buildTable(
  stats: StatsData | undefined,
  tableView: TableView,
  sortBy: string,
  sortOrder: SortOrder,
): UsageGroup[] {
  return groupRows(buildRows(stats, tableView, sortBy, sortOrder), tableView)
}

export interface ValueColumns {
  columns: { field: string; label: string }[]
}

export function getValueColumns(viewMode: ViewMode): ValueColumns['columns'] {
  if (viewMode === 'tokens') {
    return [
      { field: 'promptTokens', label: 'Input Tokens' },
      { field: 'cachedTokens', label: 'Cached' },
      { field: 'completionTokens', label: 'Output Tokens' },
      { field: 'totalTokens', label: 'Total Tokens' },
    ]
  }
  return [
    { field: 'inputCost', label: 'Input Cost' },
    { field: 'cachedCost', label: 'Cached Cost' },
    { field: 'outputCost', label: 'Output Cost' },
    { field: 'cost', label: 'Total Cost' },
  ]
}

export type CellKind = 'text' | 'badge' | 'count' | 'time' | 'money' | 'plain'

export interface Column {
  field: string
  label: string
  kind: CellKind
  align: 'left' | 'right'
  /** A summary row has no per-row value for these; it renders a dash. */
  identity: boolean
}

const MODEL_COLUMNS: Column[] = [
  { field: 'rawModel', label: 'Model', kind: 'text', align: 'left', identity: true },
  { field: 'provider', label: 'Provider', kind: 'badge', align: 'left', identity: true },
]

const ACCOUNT_COLUMNS: Column[] = [
  { field: 'rawModel', label: 'Model', kind: 'text', align: 'left', identity: true },
  { field: 'provider', label: 'Provider', kind: 'badge', align: 'left', identity: true },
  { field: 'accountName', label: 'Account', kind: 'text', align: 'left', identity: true },
]

const API_KEY_COLUMNS: Column[] = [
  { field: 'keyName', label: 'API Key Name', kind: 'text', align: 'left', identity: true },
  { field: 'rawModel', label: 'Model', kind: 'text', align: 'left', identity: true },
  { field: 'provider', label: 'Provider', kind: 'badge', align: 'left', identity: true },
]

const ENDPOINT_COLUMNS: Column[] = [
  { field: 'endpoint', label: 'Endpoint', kind: 'text', align: 'left', identity: true },
  { field: 'rawModel', label: 'Model', kind: 'text', align: 'left', identity: true },
  { field: 'provider', label: 'Provider', kind: 'badge', align: 'left', identity: true },
]

const TAIL_COLUMNS: Column[] = [
  { field: 'requests', label: 'Requests', kind: 'count', align: 'right', identity: false },
  { field: 'lastUsed', label: 'Last Used', kind: 'time', align: 'right', identity: false },
]

// The identity columns per view, mirroring upstream's MODEL_COLUMNS /
// ACCOUNT_COLUMNS / API_KEY_COLUMNS / ENDPOINT_COLUMNS. Value columns are
// appended by getColumns so the header and body share one list.
export function getIdentityColumns(tableView: TableView): Column[] {
  const base =
    tableView === 'account'
      ? ACCOUNT_COLUMNS
      : tableView === 'apiKey'
        ? API_KEY_COLUMNS
        : tableView === 'endpoint'
          ? ENDPOINT_COLUMNS
          : MODEL_COLUMNS
  return base.concat(TAIL_COLUMNS)
}

export function getColumns(tableView: TableView, viewMode: ViewMode): Column[] {
  const values = getValueColumns(viewMode).map<Column>((column) => ({
    field: column.field,
    label: column.label,
    kind: column.field === 'cost' || column.field.endsWith('Cost') ? 'money' : 'plain',
    align: 'right',
    identity: false,
  }))
  return getIdentityColumns(tableView).concat(values)
}

// Clicking a header either flips the direction or starts a new column
// ascending, matching upstream's toggleSort.
export function nextSort(current: { sortBy: string; sortOrder: SortOrder }, field: string): {
  sortBy: string
  sortOrder: SortOrder
} {
  if (current.sortBy === field) {
    return { sortBy: field, sortOrder: current.sortOrder === 'asc' ? 'desc' : 'asc' }
  }
  return { sortBy: field, sortOrder: 'asc' }
}
