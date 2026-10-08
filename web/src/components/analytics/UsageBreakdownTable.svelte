<script lang="ts">
  // Sortable, group/expandable usage breakdown, ported from upstream
  // UsageTable.js plus the per-view column sets in UsageStats.js. All row maths
  // lives in tableModel.ts so it stays testable outside a render harness.
  import Badge from '../../lib/ui/Badge.svelte'
  import Card from '../../lib/ui/Card.svelte'
  import ProviderArtwork from '../providers/ProviderArtwork.svelte'
  import {
    TABLE_EMPTY_MESSAGE,
    TABLE_STORAGE_KEY,
    buildTable,
    getColumns,
    nextSort,
    type UsageGroup,
    type UsageRow,
  } from './tableModel'
  import {
    fmt,
    fmtCost,
    timeAgo,
    TABLE_OPTIONS,
    type SortOrder,
    type StatsData,
    type TableView,
    type ViewMode,
  } from './types'

  interface Props {
    stats?: StatsData
    tableView: TableView
    viewMode: ViewMode
    sortBy: string
    sortOrder: SortOrder
    onTableViewChange: (view: TableView) => void
    onViewModeChange: (view: ViewMode) => void
    onSortChange: (sortBy: string, sortOrder: SortOrder) => void
  }

  let {
    stats = {},
    tableView,
    viewMode,
    sortBy,
    sortOrder,
    onTableViewChange,
    onViewModeChange,
    onSortChange,
  }: Props = $props()

  const VIEW_MODES: { value: ViewMode; label: string }[] = [
    { value: 'costs', label: 'Costs' },
    { value: 'tokens', label: 'Tokens' },
  ]

  let expanded = $state<Set<string>>(new Set())

  const storageKey = $derived(TABLE_STORAGE_KEY[tableView])
  const groups = $derived<UsageGroup[]>(buildTable(stats, tableView, sortBy, sortOrder))
  // One unified column list, so the header and the body can never drift apart
  // in width the way separately hand-counted cells did.
  const columns = $derived(getColumns(tableView, viewMode))

  // Expanded groups persist per view, so switching tables and returning keeps
  // the rows the user opened.
  $effect(() => {
    const key = storageKey
    try {
      const saved = window.localStorage.getItem(key)
      expanded = new Set(saved ? (JSON.parse(saved) as string[]) : [])
    } catch (error) {
      console.error(`Failed to load ${key}:`, error)
      expanded = new Set()
    }
  })

  $effect(() => {
    const key = storageKey
    const snapshot = [...expanded]
    try {
      window.localStorage.setItem(key, JSON.stringify(snapshot))
    } catch (error) {
      console.error(`Failed to save ${key}:`, error)
    }
  })

  function toggleGroup(groupKey: string): void {
    const next = new Set(expanded)
    if (next.has(groupKey)) next.delete(groupKey)
    else next.add(groupKey)
    expanded = next
  }

  function sortIndicator(field: string): string {
    if (sortBy !== field) return '↕'
    return sortOrder === 'asc' ? '↑' : '↓'
  }

  function handleSort(field: string): void {
    const next = nextSort({ sortBy, sortOrder }, field)
    onSortChange(next.sortBy, next.sortOrder)
  }

  function text(row: UsageRow | UsageGroup['summary'], field: string): string {
    const value = (row as Record<string, unknown>)[field]
    if (value === undefined || value === null || value === '') return ''
    return String(value)
  }

  function count(row: UsageRow | UsageGroup['summary'], field: string): string {
    return fmt(Number((row as Record<string, unknown>)[field] ?? 0))
  }

  function money(row: UsageRow | UsageGroup['summary'], field: string): string {
    return fmtCost(Number((row as Record<string, unknown>)[field] ?? 0))
  }
</script>

<div class="flex flex-col gap-3 pt-2">
  <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
    <select
      value={tableView}
      onchange={(event) => onTableViewChange((event.currentTarget as HTMLSelectElement).value as TableView)}
      class="w-full cursor-pointer rounded-lg border border-border bg-surface px-3 py-1.5 text-sm font-semibold text-text-main focus:outline-none focus:ring-2 focus:ring-brand-500/50 sm:w-auto"
      aria-label="Usage breakdown view"
    >
      {#each TABLE_OPTIONS as option (option.value)}
        <option value={option.value}>{option.label}</option>
      {/each}
    </select>

    <div
      class="inline-flex self-start rounded-xl border border-border bg-surface p-1 shadow-sm sm:self-auto"
      role="group"
      aria-label="Value shown"
    >
      {#each VIEW_MODES as mode (mode.value)}
        <button
          type="button"
          onclick={() => onViewModeChange(mode.value)}
          aria-pressed={viewMode === mode.value}
          class="cursor-pointer rounded-lg px-3 py-1 text-xs font-semibold transition-colors {viewMode ===
          mode.value
            ? 'bg-brand-500 text-white shadow-sm'
            : 'text-text-muted hover:bg-surface-3 hover:text-text-main'}"
        >
          {mode.label}
        </button>
      {/each}
    </div>
  </div>

  <Card padding="none" class="overflow-hidden border border-border">
    {#if groups.length === 0}
      <div class="p-8 text-center font-body text-sm text-text-muted">
        {TABLE_EMPTY_MESSAGE[tableView]}
      </div>
    {:else}
      <div class="overflow-x-auto">
        <table class="w-full border-collapse text-left text-xs font-body">
          <thead
            class="border-b border-border bg-surface-2 text-[10px] font-semibold uppercase tracking-wider text-text-muted"
          >
            <tr>
              {#each columns as column (column.field)}
                <th
                  class="cursor-pointer px-4 py-3 hover:bg-surface-3 {column.align === 'right' ? 'text-right' : ''}"
                  onclick={() => handleSort(column.field)}
                  aria-sort={sortBy === column.field ? (sortOrder === 'asc' ? 'ascending' : 'descending') : 'none'}
                >
                  {column.label}
                  <span class="ml-1 opacity-60">{sortIndicator(column.field)}</span>
                </th>
              {/each}
            </tr>
          </thead>
          <tbody class="divide-y divide-border/60">
            {#each tableData() as row}
              <tr class="hover:bg-surface-2/60 transition-colors">
                <td class="py-3 px-4 font-mono font-medium text-text-main text-xs">
                  <div class="flex items-center gap-2">
                    {#if row.provider}
                      <ProviderArtwork
                        id={row.provider}
                        alt={row.provider}
                        class="w-4 h-4 object-contain rounded shrink-0 bg-surface-2 p-0.5 border border-border/40 text-[9px] leading-none font-semibold"
                      />
                    {/if}
                    <span class="truncate">{row.rawModel || row.accountName || row.keyName || row.endpoint || row.key}</span>
                  </div>
                </td>
                <td class="py-3 px-4">
                  <div class="flex items-center gap-1.5">
                    {#if row.provider}
                      <ProviderArtwork
                        id={row.provider}
                        alt={row.provider}
                        class="w-3.5 h-3.5 object-contain rounded shrink-0 text-[9px] leading-none font-semibold"
                      />
                    {/if}
                    <Badge variant="neutral" size="sm">{row.provider || 'unknown'}</Badge>
                  </div>
                </td>
                <td class="py-3 px-4 text-right font-mono font-semibold text-text-main">
                  {fmt(row.requests)}
                </td>
                {#if viewMode === 'costs'}
                  <td class="py-3 px-4 text-right font-mono font-bold text-warning">
                    {fmtCost(row.cost)}
                  </td>
                {/each}
              </tr>

              {#if expanded.has(group.groupKey)}
                {#each group.items as item (item.key)}
                  <tr class="transition-colors hover:bg-surface-2/40">
                    {#each columns as column, index (column.field)}
                      <td
                        class="px-4 py-2.5 {column.align === 'right' ? 'text-right' : ''}"
                        class:pl-10={index === 0}
                      >
                        {#if column.kind === 'badge'}
                          <div class="flex items-center gap-1.5">
                            {#if item.provider}
                              <img
                                src={getIconPath(item.provider)}
                                alt={item.provider}
                                class="size-3.5 shrink-0 rounded-sm border border-border/40 bg-surface-2 object-contain"
                                loading="lazy"
                                onerror={(event) => {
                                  ;(event.currentTarget as HTMLElement).style.display = 'none'
                                }}
                              />
                            {/if}
                            <Badge variant={item.pending > 0 ? 'primary' : 'neutral'} size="sm">
                              {item.provider || 'unknown'}
                            </Badge>
                          </div>
                        {:else if column.kind === 'text'}
                          <span
                            class="block truncate {item.pending > 0
                              ? 'font-semibold text-primary'
                              : 'text-text-main'}"
                            title={text(item, column.field)}
                          >
                            {text(item, column.field) || '—'}
                          </span>
                        {:else if column.kind === 'count'}
                          <span class="font-mono font-semibold text-text-main">
                            {count(item, column.field)}
                          </span>
                        {:else if column.kind === 'time'}
                          <span class="whitespace-nowrap text-text-muted">{timeAgo(item.lastUsed)}</span>
                        {:else if column.kind === 'money'}
                          <span class="font-mono font-bold text-warning">
                            {money(item, column.field)}
                          </span>
                        {:else}
                          <span class="font-mono text-text-main">{count(item, column.field)}</span>
                        {/if}
                      </td>
                    {/each}
                  </tr>
                {/each}
              {/if}
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </Card>
</div>
