<script lang="ts">
  import { Play, Plus, Search, Server } from 'lucide-svelte'
  import {
    api,
    type BatchTestMode,
    type BatchTestResponse,
    type ProviderConnection,
    type ProviderNode,
  } from '../../api/client'
  import Button from '../../lib/ui/Button.svelte'
  import { PROVIDER_CATALOG, isChatProvider } from '../../lib/providers'
  import ProviderCard from './ProviderCard.svelte'
  import { getProviderStats, matchesFilter, matchesSearch } from './types'
  import {
    DEFAULT_BATCH_LABELS,
    emptyBatchResponse,
    formatBatchSummary,
    formatLatency,
    sortBatchResults,
  } from './batchTest'

  interface Props {
    connections: ProviderConnection[]
    providerNodes: ProviderNode[]
    onSelectProvider: (id: string) => void
    onToggleAll: (id: string, active: boolean) => void
    /** Opens the unified add-custom-provider dialog; the modal owns the type. */
    onAddCustom: () => void
  }

  let {
    connections = [],
    providerNodes = [],
    onSelectProvider,
    onToggleAll,
    onAddCustom,
  }: Props = $props()

  let searchQuery = $state('')
  let statusFilter = $state<'all' | 'connected' | 'error' | 'disabled' | 'not_connected'>('all')
  let showAllApikey = $state(false)

  const APIKEY_INITIAL_VISIBLE = 20

  // ─── Batch test ("Test All") ──────────────────────────────────────────────
  // The three section buttons used to call alert() and probe nothing, because
  // POST /api/providers/test-batch did not exist in the Go backend. They now
  // run the real batch and report the result per connection.
  let testingMode = $state<BatchTestMode | null>(null)
  let testingProviderId = $state<string | null>(null)
  let batchResult = $state<BatchTestResponse | null>(null)
  let batchError = $state<string | null>(null)

  const batchBusy = $derived(testingMode !== null)
  const batchRows = $derived(batchResult ? sortBatchResults(batchResult.results) : [])
  const batchSummaryText = $derived(batchResult ? formatBatchSummary(batchResult.summary) : '')

  async function runBatchTest(mode: BatchTestMode, providerIds?: string[]) {
    if (batchBusy || testingProviderId !== null) return
    testingMode = mode
    testingProviderId = null
    batchError = null
    batchResult = emptyBatchResponse(mode)

    try {
      batchResult = await api.testBatch({ mode, providerIds })
    } catch (error) {
      console.error('Batch provider test failed:', error)
      batchError = error instanceof Error ? error.message : DEFAULT_BATCH_LABELS.error
      batchResult = null
    } finally {
      testingMode = null
    }
  }

  // A single card probes only its own accounts, so the mode is the provider id
  // the endpoint reports back in testingProviderId.
  async function runProviderTest(providerId: string) {
    if (batchBusy || testingProviderId !== null) return
    testingProviderId = providerId
    batchError = null
    batchResult = emptyBatchResponse('provider')

    try {
      batchResult = await api.testBatch({ mode: 'provider', providerId })
    } catch (error) {
      console.error('Provider test failed:', error)
      batchError = error instanceof Error ? error.message : DEFAULT_BATCH_LABELS.error
      batchResult = null
    } finally {
      testingProviderId = null
    }
  }

  // 1. Custom Providers
  let customNodes = $derived(
    providerNodes
      .filter((n) => matchesSearch(n.name, searchQuery, n.id) && matchesFilter(getProviderStats(connections, n.id), statusFilter))
      .map((n) => ({ ...n, stats: getProviderStats(connections, n.id) }))
  )

  // 2. OAuth Providers
  let oauthProviders = $derived(
    PROVIDER_CATALOG
      .filter((p) => isChatProvider(p) && !p.hidden && p.category === 'oauth' && matchesSearch(p.name, searchQuery, p.id, p.alias) && matchesFilter(getProviderStats(connections, p.id, ['oauth']), statusFilter))
      .map((p) => ({ ...p, stats: getProviderStats(connections, p.id, ['oauth']) }))
      .sort((a, b) => (b.stats.connected > 0 ? 1 : 0) - (a.stats.connected > 0 ? 1 : 0) || a.name.localeCompare(b.name))
  )

  // 3. Free Tier Providers
  let freeTierProviders = $derived(
    PROVIDER_CATALOG
      .filter((p) => isChatProvider(p) && !p.hidden && (p.category === 'free' || p.category === 'freeTier') && matchesSearch(p.name, searchQuery, p.id, p.alias) && matchesFilter(getProviderStats(connections, p.id), statusFilter, p.noAuth))
      .map((p) => ({ ...p, stats: getProviderStats(connections, p.id) }))
      .sort((a, b) => (b.stats.connected > 0 || b.noAuth ? 1 : 0) - (a.stats.connected > 0 || a.noAuth ? 1 : 0) || a.name.localeCompare(b.name))
  )

  // 4. API Key Providers
  let apikeyProviders = $derived(
    PROVIDER_CATALOG
      .filter((p) => isChatProvider(p) && !p.hidden && (p.category === 'apikey' || p.category === 'webCookie') && matchesSearch(p.name, searchQuery, p.id, p.alias) && matchesFilter(getProviderStats(connections, p.id, ['apikey', 'api_key']), statusFilter))
      .map((p) => ({ ...p, stats: getProviderStats(connections, p.id, ['apikey', 'api_key']) }))
      .sort((a, b) => (b.stats.connected > 0 ? 1 : 0) - (a.stats.connected > 0 ? 1 : 0) || a.name.localeCompare(b.name))
  )

  let visibleApikeyProviders = $derived(
    showAllApikey || searchQuery.trim() !== '' || statusFilter !== 'all'
      ? apikeyProviders
      : apikeyProviders.slice(0, APIKEY_INITIAL_VISIBLE)
  )
</script>

<div class="flex flex-col gap-8 animate-fade-in">
  <!-- Search / Filter Bar -->
  <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
    <div class="relative flex-1 max-w-md">
      <Search class="w-4 h-4 text-text-muted absolute left-3 top-1/2 -translate-y-1/2" />
      <input
        type="text"
        bind:value={searchQuery}
        placeholder="Search providers..."
        class="w-full pl-9 pr-4 py-1.5 text-xs rounded-xl bg-surface border border-border text-text-main placeholder:text-text-muted focus:outline-none focus:border-brand-500 transition-colors"
      />
    </div>

    <select
      bind:value={statusFilter}
      class="h-8 rounded-lg border border-border bg-surface px-2 text-xs text-text-main outline-none transition-colors hover:border-brand-500/40 cursor-pointer"
    >
      <option value="all">All</option>
      <option value="connected">Connected</option>
      <option value="error">Error</option>
      <option value="disabled">Disabled</option>
      <option value="not_connected">Not Connected</option>
    </select>
  </div>

  <!-- 1. Custom Providers -->
  <div class="flex flex-col gap-4">
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <h2 class="text-lg sm:text-xl font-semibold leading-tight text-text-main">
        Custom Providers (OpenAI, Anthropic, Embedding)
      </h2>
      <Button size="sm" onclick={onAddCustom} class="w-full sm:w-auto">
        <Plus class="w-4 h-4 mr-1" /> Add Custom Provider
      </Button>
    </div>

    {#if customNodes.length === 0}
      <div class="flex items-center justify-center gap-2 py-4 border border-dashed border-border rounded-xl text-text-muted text-xs">
        <Server class="w-4 h-4" />
        <span>No custom providers — use “Add Custom Provider” to register an OpenAI, Anthropic, or embedding endpoint</span>
      </div>
    {:else}
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 sm:gap-4 lg:grid-cols-3 xl:grid-cols-4">
        {#each customNodes as node (node.id)}
          <ProviderCard
            id={node.id}
            name={node.name}
            apiType={node.apiType}
            stats={node.stats}
            onClick={() => onSelectProvider(node.id)}
            onToggleAll={(active) => onToggleAll(node.id, active)}
          />
        {/each}
      </div>
    {/if}
  </div>

  <!-- 2. OAuth Providers -->
  {#if oauthProviders.length > 0}
    <div class="flex flex-col gap-4">
      <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <h2 class="text-lg sm:text-xl font-semibold leading-tight text-text-main">OAuth Providers</h2>
        <Button
          size="sm"
          variant="outline"
          disabled={batchBusy}
          class="text-xs w-full sm:w-auto text-text-muted hover:text-text-main disabled:opacity-60"
          onclick={() => runBatchTest('oauth', oauthProviders.map((p) => p.id))}
        >
          <Play class="w-3.5 h-3.5 mr-1 text-text-muted" />
          {testingMode === 'oauth' ? DEFAULT_BATCH_LABELS.testing : 'Test All'}
        </Button>
      </div>
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 sm:gap-4 lg:grid-cols-3 xl:grid-cols-4">
        {#each oauthProviders as p (p.id)}
          <ProviderCard
            id={p.id}
            name={p.name}
            stats={p.stats}
            onClick={() => onSelectProvider(p.id)}
            onToggleAll={(active) => onToggleAll(p.id, active)}
            onTest={() => runProviderTest(p.id)}
            isTesting={testingProviderId === p.id}
          />
        {/each}
      </div>
    </div>
  {/if}

  <!-- 3. Free Tier Providers -->
  {#if freeTierProviders.length > 0}
    <div class="flex flex-col gap-4">
      <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <h2 class="text-lg sm:text-xl font-semibold leading-tight text-text-main">Free Tier Providers</h2>
        <Button
          size="sm"
          variant="outline"
          disabled={batchBusy}
          class="text-xs w-full sm:w-auto text-text-muted hover:text-text-main disabled:opacity-60"
          onclick={() => runBatchTest('free', freeTierProviders.map((p) => p.id))}
        >
          <Play class="w-3.5 h-3.5 mr-1 text-text-muted" />
          {testingMode === 'free' ? DEFAULT_BATCH_LABELS.testing : 'Test All'}
        </Button>
      </div>
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 sm:gap-4 lg:grid-cols-3 xl:grid-cols-4">
        {#each freeTierProviders as p (p.id)}
          <ProviderCard
            id={p.id}
            name={p.name}
            stats={p.stats}
            noAuth={p.noAuth}
            onClick={() => onSelectProvider(p.id)}
            onToggleAll={(active) => onToggleAll(p.id, active)}
            onTest={() => runProviderTest(p.id)}
            isTesting={testingProviderId === p.id}
          />
        {/each}
      </div>
    </div>
  {/if}

  <!-- 4. API Key Providers -->
  {#if apikeyProviders.length > 0}
    <div class="flex flex-col gap-4">
      <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <h2 class="text-lg sm:text-xl font-semibold leading-tight text-text-main">API Key Providers</h2>
        <Button
          size="sm"
          variant="outline"
          disabled={batchBusy}
          class="text-xs w-full sm:w-auto text-text-muted hover:text-text-main disabled:opacity-60"
          onclick={() => runBatchTest('apikey', apikeyProviders.map((p) => p.id))}
        >
          <Play class="w-3.5 h-3.5 mr-1 text-text-muted" />
          {testingMode === 'apikey' ? DEFAULT_BATCH_LABELS.testing : 'Test All'}
        </Button>
      </div>
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 sm:gap-4 lg:grid-cols-3 xl:grid-cols-4">
        {#each visibleApikeyProviders as p (p.id)}
          <ProviderCard
            id={p.id}
            name={p.name}
            stats={p.stats}
            onClick={() => onSelectProvider(p.id)}
            onToggleAll={(active) => onToggleAll(p.id, active)}
            onTest={() => runProviderTest(p.id)}
            isTesting={testingProviderId === p.id}
          />
        {/each}
      </div>
      {#if !showAllApikey && !searchQuery.trim() && statusFilter === 'all' && apikeyProviders.length > visibleApikeyProviders.length}
        <button
          type="button"
          onclick={() => (showAllApikey = true)}
          class="flex w-full items-center justify-center gap-1.5 rounded-xl border border-dashed border-border py-2.5 text-xs font-medium text-text-muted hover:text-brand-500 hover:border-brand-500/40 transition-colors cursor-pointer"
        >
          Show all {apikeyProviders.length} providers
        </button>
      {/if}
    </div>
  {/if}

  <!-- Batch test result: without this a run that probed 30 accounts and failed
       12 would look identical to one that never ran. -->
  {#if batchError || batchResult}
    <div
      class="rounded-xl border p-3 text-xs {batchError
        ? 'border-red-500/30 bg-red-500/10 text-red-600 dark:text-red-400'
        : batchResult && batchResult.summary.failed > 0
          ? 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300'
          : 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'}"
      role="status"
      aria-live="polite"
    >
      <div class="flex items-start gap-2.5">
        <span class="material-symbols-outlined shrink-0 text-base">
          {batchError
            ? 'error'
            : batchResult && batchResult.summary.failed > 0
              ? 'warning'
              : 'check_circle'}
        </span>
        <div class="min-w-0 flex-1">
          <p class="font-medium">{batchError ?? batchSummaryText}</p>

          {#if batchRows.length > 0}
            <ul class="mt-2 flex max-h-56 flex-col gap-1 overflow-y-auto">
              {#each batchRows as row (row.connectionId)}
                <li class="flex items-start gap-2">
                  <span
                    class="material-symbols-outlined mt-px shrink-0 text-[14px] {row.valid
                      ? 'text-emerald-600 dark:text-emerald-400'
                      : 'text-red-600 dark:text-red-400'}"
                  >
                    {row.valid ? 'check' : 'error'}
                  </span>
                  <span class="min-w-0 flex-1">
                    <span class="font-medium">{row.connectionName}</span>
                    <span class="text-text-muted"> · {row.provider}</span>
                    {#if !row.valid && row.error}
                      <span class="block break-words text-red-600 dark:text-red-400">{row.error}</span>
                    {/if}
                  </span>
                  <span class="shrink-0 tabular-nums text-text-muted">{formatLatency(row.latencyMs)}</span>
                </li>
              {/each}
            </ul>
          {/if}

          <button
            type="button"
            onclick={() => {
              batchResult = null
              batchError = null
            }}
            class="mt-2 cursor-pointer text-[11px] underline opacity-70 hover:opacity-100"
          >
            Dismiss
          </button>
        </div>
      </div>
    </div>
  {/if}
</div>
