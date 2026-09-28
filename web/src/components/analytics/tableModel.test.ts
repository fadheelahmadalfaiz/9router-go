import { describe, expect, it } from 'bun:test'
import {
  TABLE_EMPTY_MESSAGE,
  TABLE_STORAGE_KEY,
  buildRows,
  buildTable,
  getColumns,
  getGroupKey,
  getIdentityColumns,
  getValueColumns,
  groupRows,
  nextSort,
  type UsageRow,
} from './tableModel'
import type { StatsData, TableView } from './types'

const stats: StatsData = {
  byModel: {
    'gpt-5 (codex)': {
      requests: 10,
      promptTokens: 1000,
      completionTokens: 200,
      cachedTokens: 400,
      cost: 1,
      rawModel: 'gpt-5',
      provider: 'codex',
      lastUsed: '2026-09-01T10:00:00Z',
    },
    'claude-sonnet (claude)': {
      requests: 4,
      promptTokens: 500,
      completionTokens: 100,
      cachedTokens: 0,
      cost: 0.5,
      rawModel: 'claude-sonnet',
      provider: 'claude',
      lastUsed: '2026-09-02T10:00:00Z',
    },
  },
  pending: {
    byModel: { 'gpt-5 (codex)': 2 },
  },
}

describe('buildRows', () => {
  it('computes total tokens', () => {
    const rows = buildRows(stats, 'model', 'requests', 'desc')
    expect(rows[0].rawModel).toBe('gpt-5')
    expect(rows[0].totalTokens).toBe(1200)
  })

  it('sorts ascending by the requested field', () => {
    const rows = buildRows(stats, 'model', 'requests', 'asc')
    expect(rows.map((r) => r.rawModel)).toEqual(['claude-sonnet', 'gpt-5'])
  })

  it('sorts descending by the requested field', () => {
    const rows = buildRows(stats, 'model', 'requests', 'desc')
    expect(rows.map((r) => r.rawModel)).toEqual(['gpt-5', 'claude-sonnet'])
  })

  it('sorts string fields case-insensitively', () => {
    const rows = buildRows(stats, 'model', 'rawModel', 'asc')
    expect(rows.map((r) => r.rawModel)).toEqual(['claude-sonnet', 'gpt-5'])
  })

  it('splits cost by token share, peeling cached out of the input share', () => {
    const row = buildRows(stats, 'model', 'requests', 'desc')[0]
    const total = 1200
    expect(row.cachedCost).toBeCloseTo((400 * 1) / total, 10)
    expect(row.outputCost).toBeCloseTo((200 * 1) / total, 10)
    // input share is prompt-minus-cached, so the three parts still sum to cost
    expect(row.inputCost + row.cachedCost + row.outputCost).toBeCloseTo(1, 10)
  })

  it('leaves the cost split at zero when there are no tokens', () => {
    const rows = buildRows({ byModel: { a: { cost: 5, requests: 1 } } }, 'model', 'key', 'asc')
    expect(rows[0].inputCost).toBe(0)
    expect(rows[0].cachedCost).toBe(0)
    expect(rows[0].outputCost).toBe(0)
  })

  it('reads pending from pending.byModel for the model view', () => {
    const rows = buildRows(stats, 'model', 'requests', 'desc')
    expect(rows[0].pending).toBe(2)
    expect(rows[1].pending).toBe(0)
  })

  it('reads pending through the connectionId and composite model key for the account view', () => {
    const accountStats: StatsData = {
      byAccount: {
        'gpt-5 (codex - Work)': {
          requests: 1,
          rawModel: 'gpt-5',
          provider: 'codex',
          accountName: 'Work',
          connectionId: 'conn-1',
        },
      },
      pending: { byAccount: { 'conn-1': { 'gpt-5 (codex)': 3 } } },
    }
    const rows = buildRows(accountStats, 'account', 'requests', 'asc')
    expect(rows[0].pending).toBe(3)
  })

  it('reports no pending for views the backend does not track', () => {
    const rows = buildRows(stats, 'endpoint', 'requests', 'asc')
    expect(rows).toHaveLength(0)
  })

  it('pushes blank cells to the end in ascending order', () => {
    const rows = buildRows(
      { byModel: { a: { requests: 5 }, b: { requests: 1, lastUsed: 'x' } } },
      'model',
      'lastUsed',
      'asc',
    )
    expect(rows[0].lastUsed).toBe('x')
  })
})

describe('getGroupKey', () => {
  const row = { key: 'fallback', rawModel: 'gpt-5', accountName: 'Work', keyName: 'CLI', endpoint: '/v1' }

  it('uses the per-view label', () => {
    expect(getGroupKey(row, 'model')).toBe('gpt-5')
    expect(getGroupKey(row, 'account')).toBe('Work')
    expect(getGroupKey(row, 'apiKey')).toBe('CLI')
    expect(getGroupKey(row, 'endpoint')).toBe('/v1')
  })

  it('falls back when the label is missing', () => {
    expect(getGroupKey({ key: 'k' }, 'model')).toBe('Unknown Model')
    expect(getGroupKey({ key: 'k' }, 'account')).toBe('k')
    expect(getGroupKey({}, 'apiKey')).toBe('Unknown Key')
    expect(getGroupKey({}, 'endpoint')).toBe('Unknown Endpoint')
  })
})

describe('groupRows', () => {
  it('aggregates the summary across a group', () => {
    const rows = buildRows(stats, 'model', 'requests', 'asc')
    const groups = groupRows(rows, 'model')
    const gpt = groups.find((g) => g.groupKey === 'gpt-5')
    expect(gpt!.summary.requests).toBe(10)
    expect(gpt!.summary.totalTokens).toBe(1200)
    expect(gpt!.summary.pending).toBe(2)
  })

  it('keeps the latest lastUsed in the summary', () => {
    const rows = [
      { key: 'a', rawModel: 'm', lastUsed: '2026-01-01T00:00:00Z' },
      { key: 'b', rawModel: 'm', lastUsed: '2026-09-09T00:00:00Z' },
    ] as UsageRow[]
    const groups = groupRows(rows, 'model')
    expect(groups[0].summary.lastUsed).toBe('2026-09-09T00:00:00Z')
  })

  it('merges rows that share a group key into one expandable group', () => {
    const rows = [
      { key: 'a', rawModel: 'm', requests: 1 },
      { key: 'b', rawModel: 'm', requests: 2 },
    ] as UsageRow[]
    const groups = groupRows(rows, 'model')
    expect(groups).toHaveLength(1)
    expect(groups[0].items).toHaveLength(2)
  })

  it('returns no groups for missing stats', () => {
    expect(buildTable(undefined, 'model', 'rawModel', 'asc')).toEqual([])
    expect(buildTable({}, 'apiKey', 'keyName', 'asc')).toEqual([])
  })
})

describe('getValueColumns', () => {
  it('returns the token columns in tokens mode', () => {
    expect(getValueColumns('tokens').map((c) => c.field)).toEqual([
      'promptTokens',
      'cachedTokens',
      'completionTokens',
      'totalTokens',
    ])
  })

  it('returns the cost columns in costs mode', () => {
    expect(getValueColumns('costs').map((c) => c.field)).toEqual([
      'inputCost',
      'cachedCost',
      'outputCost',
      'cost',
    ])
  })
})

describe('nextSort', () => {
  it('starts a new column ascending', () => {
    expect(nextSort({ sortBy: 'rawModel', sortOrder: 'desc' }, 'cost')).toEqual({
      sortBy: 'cost',
      sortOrder: 'asc',
    })
  })

  it('flips the direction on the same column', () => {
    expect(nextSort({ sortBy: 'cost', sortOrder: 'asc' }, 'cost').sortOrder).toBe('desc')
    expect(nextSort({ sortBy: 'cost', sortOrder: 'desc' }, 'cost').sortOrder).toBe('asc')
  })
})

describe('table metadata', () => {
  it('gives every view a distinct localStorage key', () => {
    const keys = Object.values(TABLE_STORAGE_KEY)
    expect(new Set(keys).size).toBe(keys.length)
  })

  it('gives every view a distinct empty message', () => {
    const messages = Object.values(TABLE_EMPTY_MESSAGE)
    expect(new Set(messages).size).toBe(messages.length)
  })
})

describe('getColumns', () => {
  const views: TableView[] = ['model', 'account', 'apiKey', 'endpoint']

  it('ends every view with the same requests and last-used columns', () => {
    for (const view of views) {
      const fields = getColumns(view, 'costs').map((column) => column.field)
      expect(fields.slice(-6, -4)).toEqual(['requests', 'lastUsed'])
    }
  })

  it('sizes each view as its identity columns plus the four value columns', () => {
    // Upstream's MODEL_COLUMNS has four entries while the other three have
    // five, so the widths are deliberately not all equal.
    for (const view of views) {
      const identity = getIdentityColumns(view)
      expect(getColumns(view, 'costs')).toHaveLength(identity.length + 4)
      expect(getColumns(view, 'tokens')).toHaveLength(identity.length + 4)
    }
    expect(getIdentityColumns('model')).toHaveLength(4)
    for (const view of ['account', 'apiKey', 'endpoint'] as TableView[]) {
      expect(getIdentityColumns(view)).toHaveLength(5)
    }
  })

  it('marks exactly the leading identity columns as identity', () => {
    const identity = getIdentityColumns('account').filter((column) => column.identity)
    expect(identity.map((column) => column.field)).toEqual(['rawModel', 'provider', 'accountName'])
  })

  it('leads each view with its own label column', () => {
    expect(getColumns('apiKey', 'tokens')[0].field).toBe('keyName')
    expect(getColumns('endpoint', 'tokens')[0].field).toBe('endpoint')
    expect(getColumns('model', 'tokens')[0].field).toBe('rawModel')
  })

  it('classifies cost columns as money and token columns as plain', () => {
    const costs = getColumns('model', 'costs')
    expect(costs.find((column) => column.field === 'inputCost')!.kind).toBe('money')
    const tokens = getColumns('model', 'tokens')
    expect(tokens.find((column) => column.field === 'promptTokens')!.kind).toBe('plain')
  })

  it('never repeats a field within a view', () => {
    for (const view of views) {
      for (const mode of ['costs', 'tokens'] as const) {
        const fields = getColumns(view, mode).map((column) => column.field)
        expect(new Set(fields).size).toBe(fields.length)
      }
    }
  })

  it('does not let one view\'s column table leak into another', () => {
    const account = getIdentityColumns('account')
    const model = getIdentityColumns('model')
    expect(account).not.toBe(model)
    expect(account).toHaveLength(model.length + 1)
  })
})
