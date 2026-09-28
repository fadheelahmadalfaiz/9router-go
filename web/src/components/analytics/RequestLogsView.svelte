<script lang="ts">
  // Port of upstream src/shared/components/RequestLogger.js. Row parsing lives
  // in requestLogs.ts so it stays testable outside a render harness.
  import { api } from '../../api/client'
  import Card from '../../lib/ui/Card.svelte'
  import { getRequestLogStatus, parseRequestLogs, type RequestLogRow } from './requestLogs'

  const POLL_MS = 3000

  let rows = $state<RequestLogRow[]>([])
  let loading = $state(true)
  let autoRefresh = $state(true)

  async function fetchLogs(showLoading = true): Promise<void> {
    if (showLoading) loading = true
    try {
      rows = parseRequestLogs(await api.getUsageRequestLogs())
    } catch (error) {
      console.error('Failed to fetch request logs:', error)
    } finally {
      if (showLoading) loading = false
    }
  }

  // Poll only while the tab is visible, the same guard the usage page uses.
  $effect(() => {
    void fetchLogs()
    if (!autoRefresh) return

    const timer = setInterval(() => {
      if (typeof document !== 'undefined' && document.hidden) return
      void fetchLogs(false)
    }, POLL_MS)
    return () => clearInterval(timer)
  })
</script>

<div class="flex flex-col gap-4">
  <div class="flex items-center justify-between gap-2">
    <h2 class="text-xl font-semibold text-text-main">Request Logs</h2>
    <div class="flex items-center gap-2">
      <span class="hidden text-sm font-medium text-text-muted sm:inline">Auto Refresh (3s)</span>
      <button
        type="button"
        role="switch"
        aria-checked={autoRefresh}
        aria-label="Toggle auto refresh"
        onclick={() => (autoRefresh = !autoRefresh)}
        class="relative inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full transition-colors {autoRefresh
          ? 'bg-primary'
          : 'border border-border bg-surface-2'}"
      >
        <span
          class="inline-block size-3 transform rounded-full bg-white transition-transform {autoRefresh
            ? 'translate-x-5'
            : 'translate-x-1'}"
        ></span>
      </button>
    </div>
  </div>

  <Card padding="none" class="overflow-hidden">
    <div class="max-h-[600px] overflow-auto font-mono text-xs">
      {#if loading && rows.length === 0}
        <div class="p-8 text-center text-text-muted">Loading logs…</div>
      {:else if rows.length === 0}
        <div class="p-8 text-center text-text-muted">No logs recorded yet.</div>
      {:else}
        <table class="w-full border-collapse whitespace-nowrap text-left">
          <thead class="sticky top-0 z-10 border-b border-border bg-surface-2">
            <tr>
              <th class="border-r border-border px-3 py-2">DateTime</th>
              <th class="border-r border-border px-3 py-2">Model</th>
              <th class="border-r border-border px-3 py-2">Provider</th>
              <th class="border-r border-border px-3 py-2">Account</th>
              <th class="border-r border-border px-3 py-2 text-right">In</th>
              <th class="border-r border-border px-3 py-2 text-right">Out</th>
              <th class="px-3 py-2">Status</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border/50">
            {#each rows as row, index (index)}
              {@const status = getRequestLogStatus(row.status)}
              <tr class="transition-colors hover:bg-primary/5 {status === 'pending' ? 'bg-primary/5' : ''}">
                <td class="border-r border-border px-3 py-1.5 text-text-muted">{row.date}</td>
                <td class="border-r border-border px-3 py-1.5 font-medium text-text-main">{row.model}</td>
                <td class="border-r border-border px-3 py-1.5">
                  <span
                    class="rounded border border-border bg-surface-2 px-1.5 py-0.5 text-[10px] font-bold uppercase text-text-muted"
                  >
                    {row.provider}
                  </span>
                </td>
                <td class="max-w-[150px] truncate border-r border-border px-3 py-1.5" title={row.account}>
                  {row.account}
                </td>
                <td class="border-r border-border px-3 py-1.5 text-right text-primary">{row.tokensIn}</td>
                <td class="border-r border-border px-3 py-1.5 text-right text-success">{row.tokensOut}</td>
                <td
                  class="px-3 py-1.5 font-bold {status === 'ok'
                    ? 'text-success'
                    : status === 'failed'
                      ? 'text-error'
                      : 'text-primary'}"
                >
                  {row.status}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}
    </div>
  </Card>
  <p class="text-[10px] italic text-text-muted">Logs are loaded from the request history database.</p>
</div>
