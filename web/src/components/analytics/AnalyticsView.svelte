<script lang="ts">
  import { onMount } from 'svelte'
  import { api, getAuthHeaders, type ProviderConnection, type ProviderNode } from '../../api/client'
  import { pathToTab } from '../../lib/router'
  import {
    fmt,
    timeAgo,
    PERIODS,
    type MainTab,
    type Period,
    type StatsData,
    type RequestDetailItem,
    type SortOrder,
    type TableView,
    type ViewMode,
    type ActiveRequestItem
  } from './types'
  import {
    USAGE_URL_DEFAULTS,
    buildUsageUrl,
    parseUsageUrlState,
    type UsageUrlState,
  } from './urlState'
  import { buildTopologyProviders, type TopologyProvider } from './topology'
  import SummaryKpiCards from './SummaryKpiCards.svelte'
  import RecentRequestsCard from './RecentRequestsCard.svelte'
  import UsageBreakdownTable from './UsageBreakdownTable.svelte'
  import RequestDetailsTab from './RequestDetailsTab.svelte'
  import RequestLogsView from './RequestLogsView.svelte'
  import ProviderTopologyCard from './ProviderTopologyCard.svelte'
  import type { Component } from 'svelte'
  import type { UsageChartPoint } from '../../api/client'

  // The chart library is a large dependency and this view is one tab out of
  // many, so the three charts load only when the overview actually renders.
  // Importing them statically charged every dashboard page for them.
  type ChartModules = {
    Usage: Component<{ period: Period }>
    Provider: Component<{ byProvider?: StatsData['byProvider'] }>
    Top: Component<{ byModel?: StatsData['byModel'] }>
  }

  let charts = $state<ChartModules | null>(null)

  onMount(async () => {
    const [usage, provider, top] = await Promise.all([
      import('./charts/UsageChart.svelte'),
      import('./charts/ProviderBarChart.svelte'),
      import('./charts/TopModelsChart.svelte'),
    ])
    charts = {
      Usage: usage.default as ChartModules['Usage'],
      Provider: provider.default as ChartModules['Provider'],
      Top: top.default as ChartModules['Top'],
    }
  })
  interface Props {
    connections?: ProviderConnection[]
    providerNodes?: ProviderNode[]
  }

  let { connections = [], providerNodes = [] }: Props = $props()

  // Tab, period, table view and sort order live in the query string, so a
  // reload, a shared link and back/forward all restore the same view. Parsing
  // happens at component init, before the first fetch, so the initial request
  // already uses the URL's period.
  const initialUrlState: UsageUrlState =
    typeof window !== 'undefined' ? parseUsageUrlState(window.location.search) : USAGE_URL_DEFAULTS
  let activeTab = $state<MainTab>(initialUrlState.tab)
  let period = $state<Period>(initialUrlState.period)
  let tableView = $state<TableView>(initialUrlState.table)
  let viewMode = $state<ViewMode>(initialUrlState.view)
  let sortBy = $state(initialUrlState.sortBy)
  let sortOrder = $state<SortOrder>(initialUrlState.sortOrder)
  let isFetching = $state(false)
  let stats = $state<StatsData>({})
  let activeRequests = $state<ActiveRequestItem[]>([])
  let pulseProvider = $state<string>('')
  let lastProvider = $state<string>('')
  let errorProvider = $state<string>('')
  let pulseTimer: ReturnType<typeof setTimeout> | null = null

  function triggerPulse(provider: string) {
    if (!provider) return
    pulseProvider = provider
    if (pulseTimer) clearTimeout(pulseTimer)
    pulseTimer = setTimeout(() => {
      pulseProvider = ''
    }, 3000)
  }
  // mergeRecent unions an incoming SSE list with what is on screen. The SSE
  // stream carries only this process's in-memory ring, so a plain replace
  // collapses the DB-backed list (20 rows after a REST load) down to the few
  // rows seen since the last restart — the list visibly blinks and rows below
  // vanish. Union + dedupe + newest-first keeps rows the stream has not seen.
  function mergeRecent(
    prev: RecentRequestItem[] | undefined,
    next: RecentRequestItem[] | undefined,
  ): RecentRequestItem[] {
    const byKey = new Map<string, RecentRequestItem>()
    const keyOf = (r: RecentRequestItem) =>
      `${r.model}|${r.provider}|${r.promptTokens}|${r.completionTokens}|${(r.timestamp || '').slice(0, 16)}`
    for (const r of prev || []) byKey.set(keyOf(r), r)
    for (const r of next || []) byKey.set(keyOf(r), r)
    return [...byKey.values()]
      .sort((a, b) => (b.timestamp || '').localeCompare(a.timestamp || ''))
      .slice(0, 20)
  }
  // Request details tab state
  let details = $state<RequestDetailItem[]>([])
  let detailsTotal = $state(0)
  let detailsPage = $state(1)
  let detailsLoading = $state(false)
  async function loadStats(targetPeriod: Period) {
    isFetching = true
    try {
      const res = await api.getUsageStats(targetPeriod)
      if (res) {
        stats = res
        if (Array.isArray(res.activeRequests)) {
          activeRequests = res.activeRequests
        }
        if (!lastProvider) {
          if (Array.isArray(res.activeRequests) && res.activeRequests.length > 0 && res.activeRequests[0].provider) {
            lastProvider = res.activeRequests[0].provider
          } else if (Array.isArray(res.recentRequests) && res.recentRequests.length > 0) {
            lastProvider = res.recentRequests[0].provider || ''
          }
        }
        if (res.errorProvider) {
          errorProvider = res.errorProvider
        }
      }
    } catch (err) {
      console.error('Failed to load usage stats:', err)
    } finally {
      isFetching = false
    }
  }

  async function loadDetails(page = 1) {
    detailsLoading = true
    try {
      const limit = 20
      const offset = (page - 1) * limit
      const res = await api.getRequestDetails(limit, offset)
      if (res && Array.isArray(res.details)) {
        details = res.details
        detailsTotal = res.total || 0
        detailsPage = page
      }
    } catch (err) {
      console.error('Failed to load request details:', err)
    } finally {
      detailsLoading = false
    }
  }

  $effect(() => {
    loadStats(period)
  })

  $effect(() => {
    if (activeTab === 'details') {
      loadDetails(detailsPage)
    }
  })

  // SSE real-time updates for activeRequests, recentRequests and error notifications
  $effect(() => {
    let isCancelled = false
    let controller: AbortController | null = null
    let reconnectTimeout: ReturnType<typeof setTimeout> | null = null

    const token = typeof localStorage !== 'undefined' ? localStorage.getItem('9router_key') || '' : ''
    let streamInitialized = false

    const connectStream = async () => {
      if (isCancelled) return
      controller = new AbortController()

      try {
        const streamUrl = token ? `/api/usage/stream?key=${encodeURIComponent(token)}` : '/api/usage/stream'
        const res = await fetch(streamUrl, {
          headers: getAuthHeaders(),
          signal: controller.signal,
        })

        if (!res.ok) {
          throw new Error(`usage stream failed: ${res.status}`)
        }

        const reader = res.body?.getReader()
        const decoder = new TextDecoder()
        if (!reader) return

        let buffer = ''
        while (!isCancelled) {
          const { done, value } = await reader.read()
          if (done) break

          buffer += decoder.decode(value, { stream: true })
          const lines = buffer.split('\n')
          buffer = lines.pop() || ''

          for (const line of lines) {
            const trimmed = line.trim()
            if (!trimmed || trimmed.startsWith(':')) continue
            if (!trimmed.startsWith('data: ')) continue

            try {
              const data = JSON.parse(trimmed.slice(6))
              if (Array.isArray(data.recentRequests) && data.recentRequests.length > 0) {
                const prevTop = stats.recentRequests?.[0]
                const newTop = data.recentRequests[0]
                // Only pulse animation when a GENUINE new model request arrives AFTER stream initialization
                if (streamInitialized && prevTop) {
                  const isNewRequest =
                    newTop.timestamp !== prevTop.timestamp ||
                    newTop.model !== prevTop.model ||
                    newTop.tokens !== prevTop.tokens
                  if (isNewRequest && newTop.provider) {
                    lastProvider = newTop.provider
                    triggerPulse(newTop.provider)
                  }
                } else if (!lastProvider && newTop.provider) {
                  lastProvider = newTop.provider
                }
                streamInitialized = true
                stats = { ...stats, recentRequests: mergeRecent(stats.recentRequests, data.recentRequests) }
              }
              if (Array.isArray(data.activeRequests)) {
                activeRequests = data.activeRequests
                stats = { ...stats, activeRequests: data.activeRequests }
                if (data.activeRequests.length > 0 && data.activeRequests[0].provider) {
                  lastProvider = data.activeRequests[0].provider
                }
              }
              if (data.errorProvider) {
                errorProvider = data.errorProvider
              }
            } catch (err) {
              console.error('Failed to parse SSE usage stream:', err)
            }
          }
        }
      } catch (err) {
        if (!isCancelled) {
          reconnectTimeout = setTimeout(connectStream, 3000)
        }
      }
    }

    connectStream()

    // Auto-poll stats every 5s so KPI counters smoothly increment in real time
    const pollTimer = setInterval(() => {
      if (activeTab === 'overview' && (typeof document === 'undefined' || !document.hidden)) {
        loadStats(period)
      }
    }, 5000)

    return () => {
      isCancelled = true
      if (controller) controller.abort()
      if (reconnectTimeout) clearTimeout(reconnectTimeout)
      if (pulseTimer) clearTimeout(pulseTimer)
      clearInterval(pollTimer)
    }
  })

  // Back/forward across filter changes: App.svelte only re-reads the pathname,
  // so the query string is applied here.
  onMount(() => {
    window.addEventListener('popstate', restoreUrlState)
    return () => window.removeEventListener('popstate', restoreUrlState)
  })
  let topologyProviders = $derived.by<TopologyProvider[]>(() =>
    buildTopologyProviders({
      connections,
      providerNodes,
      activeRequests,
      recentRequests: stats.recentRequests || [],
      pulseProvider,
      lastProvider,
      errorProvider,
      byProvider: stats.byProvider,
    }),
  )

  // ─── URL state sync ────────────────────────────────────────────────────────
  function syncUrl(nextState: UsageUrlState): void {
    if (typeof window === 'undefined') return
    // Only ever touch the analytics route; other tabs own their own URLs.
    if (pathToTab(window.location.pathname) !== 'analytics') return

    const search = buildUsageUrl(nextState, window.location.search)
    if (search === window.location.search) return

    const url = `${window.location.pathname}${search}`
    window.history.replaceState({ tab: 'analytics', usage: nextState }, '', url)
  }

  $effect(() => {
    syncUrl({ tab: activeTab, period, table: tableView, view: viewMode, sortBy, sortOrder })
  })

  function restoreUrlState(): void {
    const restored = parseUsageUrlState(window.location.search)
    activeTab = restored.tab
    period = restored.period
    tableView = restored.table
    viewMode = restored.view
    sortBy = restored.sortBy
    sortOrder = restored.sortOrder
  }

  function handleTabChange(tab: MainTab): void {
    activeTab = tab
  }

  function handleSortChange(nextSortBy: string, nextSortOrder: SortOrder): void {
    sortBy = nextSortBy
    sortOrder = nextSortOrder
  }
</script>

<div class="flex min-w-0 flex-col gap-6 px-1 sm:px-0">
  <!-- Tabs + Period Selector Row -->
  <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
    <div class="inline-flex rounded-xl bg-surface border border-border p-1 shadow-sm" role="tablist" aria-label="Usage views">
      {#each [{ value: 'overview', label: 'Overview' }, { value: 'details', label: 'Details' }, { value: 'logs', label: 'Logs' }] as const as tab (tab.value)}
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === tab.value}
          onclick={() => handleTabChange(tab.value)}
          class="rounded-lg px-4 py-1.5 text-xs sm:text-sm font-medium transition-colors cursor-pointer {activeTab ===
          tab.value
            ? 'bg-brand-500 text-white font-semibold shadow-sm'
            : 'text-text-muted hover:text-text-main'}"
        >
          {tab.label}
        </button>
      {/each}
    </div>

    {#if activeTab === 'overview'}
      <div class="flex items-center gap-1.5 self-start sm:self-auto">
        <div class="inline-flex rounded-xl bg-surface border border-border p-1 shadow-sm">
          {#each PERIODS as p}
            <button
              type="button"
              onclick={() => (period = p.value)}
              disabled={isFetching}
              class="rounded-lg px-3 py-1 text-xs sm:text-sm font-medium transition-colors cursor-pointer {period === p.value
                ? 'bg-brand-500 text-white font-semibold shadow-sm'
                : 'text-text-muted hover:text-text-main'}"
            >
              {p.label}
            </button>
          {/each}
        </div>
        {#if isFetching}
          <span class="w-2 h-2 rounded-full bg-brand-500 animate-ping"></span>
        {/if}
      </div>
    {/if}
  </div>

  {#if activeTab === 'overview'}
    <!-- 5 Overview KPI Cards -->
    <SummaryKpiCards {stats} />

    <!-- Topology + Recent Requests -->
    <div class="grid min-w-0 grid-cols-1 items-stretch gap-2 lg:grid-cols-[minmax(0,2fr)_minmax(280px,1fr)]">
      <ProviderTopologyCard
        providers={topologyProviders}
        {activeRequests}
        {pulseProvider}
        {lastProvider}
        {errorProvider}
        onRefresh={() => loadStats(period)}
      />
      <RecentRequestsCard requests={stats.recentRequests || []} />
    </div>

    <!-- Token / cost time series, synced to the selected period -->
    {#if charts}
      <charts.Usage {period} />

      <!-- Provider and model breakdown charts -->
      {#if stats.byProvider || stats.byModel}
        <div class="grid min-w-0 grid-cols-1 gap-2 lg:grid-cols-2">
          <charts.Provider byProvider={stats.byProvider} />
          <charts.Top byModel={stats.byModel} />
        </div>
      {/if}
    {/if}

    <!-- Breakdown Table -->
    <UsageBreakdownTable
      {stats}
      {tableView}
      {viewMode}
      {sortBy}
      {sortOrder}
      onTableViewChange={(view) => (tableView = view)}
      onViewModeChange={(view) => (viewMode = view)}
      onSortChange={handleSortChange}
    />
  {:else if activeTab === 'details'}
    <RequestDetailsTab
      {details}
      {detailsTotal}
      {detailsPage}
      {detailsLoading}
      onPageChange={loadDetails}
      onRefresh={() => loadDetails(detailsPage)}
    />
  {:else}
    <RequestLogsView />
  {/if}
</div>
