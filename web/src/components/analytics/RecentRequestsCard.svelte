<script lang="ts">
  // The "Recent Requests" card beside the topology. Extracted from
  // AnalyticsView so that view stays within the file-size budget; the markup
  // is unchanged from before the split.
  import { fmt, timeAgo, type RecentRequestItem } from './types'

  interface Props {
    requests?: RecentRequestItem[]
  }

  let { requests = [] }: Props = $props()
</script>

<div
  class="flex min-w-0 flex-col overflow-hidden rounded-[14px] border border-border-subtle bg-surface p-4 shadow-[var(--shadow-soft)]"
  style="height: 480px"
>
  <div class="shrink-0 border-b border-border px-1 py-2">
    <span class="text-xs font-semibold uppercase tracking-wide text-text-muted">Recent Requests</span>
  </div>

  {#if requests.length === 0}
    <div class="flex flex-1 items-center justify-center text-xs text-text-muted">No requests recorded yet.</div>
  {:else}
    <div class="flex-1 overflow-y-auto">
      <table class="w-full table-fixed min-w-[280px] border-collapse text-xs">
        <colgroup>
          <col class="w-[20px]" />
          <col />
          <col class="w-[96px]" />
          <col class="w-[56px]" />
        </colgroup>
        <thead class="sticky top-0 z-10 bg-bg">
          <tr class="border-b border-border">
            <th class="py-1.5 pl-3 text-left font-semibold text-text-muted"></th>
            <th class="py-1.5 text-left font-semibold text-text-muted">Model</th>
            <th class="py-1.5 whitespace-nowrap text-right font-semibold text-text-muted">In / Out</th>
            <th class="py-1.5 whitespace-nowrap pr-3 text-right font-semibold text-text-muted">When</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border/50 font-mono text-[11px]">
          {#each requests as request (request.timestamp + request.model)}
            <tr class="transition-colors hover:bg-bg-subtle">
              <td class="py-1.5 pl-3 align-middle">
                <span
                  class="mx-auto block size-1.5 rounded-full {request.status === 'ok' ||
                  request.status === 'success'
                    ? 'bg-success'
                    : 'bg-error'}"
                ></span>
              </td>
              <td class="min-w-0 py-1.5 pr-2">
                <span class="block truncate font-mono text-[11px]" title={request.model}>
                  {request.model}
                </span>
              </td>
              <td class="whitespace-nowrap py-1.5 pr-3 text-right">
                <span class="text-primary">{fmt(request.promptTokens)}↑</span>
                <span class="text-success">{fmt(request.completionTokens)}↓</span>
              </td>
              <td class="whitespace-nowrap py-1.5 pr-3 text-right text-[10px] text-text-muted">
                {timeAgo(request.timestamp)}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>
