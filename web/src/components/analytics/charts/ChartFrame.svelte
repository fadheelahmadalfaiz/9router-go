<script lang="ts">
  import type { Snippet } from 'svelte'

  interface Props {
    title?: string
    /** Rendered in the header, right-aligned (the view-mode toggle). */
    controls?: Snippet
    loading?: boolean
    empty?: boolean
    emptyMessage?: string
    height?: number
    children: Snippet
  }

  let {
    title = '',
    controls,
    loading = false,
    empty = false,
    emptyMessage = 'No data for this period',
    height = 220,
    children,
  }: Props = $props()
</script>

<div class="flex min-w-0 flex-col gap-3 rounded-[14px] border border-border-subtle bg-surface p-3 shadow-[var(--shadow-soft)] sm:p-4">
  {#if title || controls}
    <div class="flex items-center justify-between gap-2">
      {#if title}
        <span class="text-sm font-semibold uppercase tracking-wide text-text-muted">{title}</span>
      {:else}
        <span></span>
      {/if}
      {#if controls}
        {@render controls()}
      {/if}
    </div>
  {/if}

  <div style="height: {height}px">
    {#if loading}
      <div class="flex h-full items-center justify-center text-sm text-text-muted">Loading…</div>
    {:else if empty}
      <div class="flex h-full items-center justify-center text-sm text-text-muted">{emptyMessage}</div>
    {:else}
      {@render children()}
    {/if}
  </div>
</div>
