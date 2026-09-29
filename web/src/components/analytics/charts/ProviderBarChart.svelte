<script lang="ts">
  // ProviderBarChart.js (recharts BarChart, vertical orientation). Hand-rolled
  // SVG — see scale.ts for why the LayerChart port was dropped.
  import type { UsageItem } from '../types'
  import ChartFrame from './ChartFrame.svelte'
  import { fmtRequests, fmtTokens, truncateLabel } from './formatters'
  import { bandScale, linearScale, niceMax, niceTicks } from './scale'

  interface Props {
    byProvider?: Record<string, UsageItem>
  }

  let { byProvider = {} }: Props = $props()

  type ViewMode = 'tokens' | 'requests'

  const BAR_COLOR = '#6366f1'
  const LABEL_MAX = 18
  const TOP_N = 10

  const VIEW_CONFIG: Record<ViewMode, { format: (n: number) => string; label: string }> = {
    tokens: { format: fmtTokens, label: 'Tokens' },
    requests: { format: fmtRequests, label: 'Requests' },
  }

  const VIEW_W = 420
  const HEIGHT = 180
  const PAD = { top: 6, right: 34, bottom: 18, left: 96 }

  let viewMode = $state<ViewMode>('tokens')

  const config = $derived(VIEW_CONFIG[viewMode])

  const chartData = $derived(
    Object.entries(byProvider || {})
      .map(([id, item]) => ({
        id,
        label: truncateLabel(id, LABEL_MAX),
        tokens: (item.promptTokens || 0) + (item.completionTokens || 0),
        requests: item.requests || 0,
      }))
      .filter((row) => row[viewMode] > 0)
      .sort((a, b) => b[viewMode] - a[viewMode])
      .slice(0, TOP_N),
  )

  const plotW = VIEW_W - PAD.left - PAD.right
  const plotH = HEIGHT - PAD.top - PAD.bottom
  const maxValue = $derived(niceMax(Math.max(0, ...chartData.map((r) => r[viewMode]))))
  const xScale = $derived(linearScale([0, maxValue], [PAD.left, PAD.left + plotW]))
  const yBands = $derived(bandScale(chartData.length, [PAD.top, PAD.top + plotH], 0.3))
  const xTicks = $derived(niceTicks(maxValue, 4))
</script>

<ChartFrame title="By Provider" empty={chartData.length === 0} emptyMessage="No provider usage yet" height={HEIGHT}>
  {#snippet controls()}
    <div class="grid grid-cols-2 items-center gap-1 rounded-lg border border-border bg-surface-2 p-1">
      {#each ['tokens', 'requests'] as const as mode (mode)}
        <button
          type="button"
          onclick={() => (viewMode = mode)}
          class="cursor-pointer rounded-md px-2.5 py-0.5 text-xs font-medium transition-colors {viewMode === mode
            ? 'bg-primary text-white shadow-sm'
            : 'text-text-muted hover:bg-surface-3 hover:text-text-main'}"
        >
          {mode === 'tokens' ? 'Tokens' : 'Requests'}
        </button>
      {/each}
    </div>
  {/snippet}

  <svg
    viewBox="0 0 {VIEW_W} {HEIGHT}"
    class="h-full w-full"
    preserveAspectRatio="none"
    role="img"
    aria-label="{config.label} by provider"
  >
    {#each xTicks as tick (tick)}
      <line
        x1={xScale(tick)}
        x2={xScale(tick)}
        y1={PAD.top}
        y2={PAD.top + plotH}
        stroke="currentColor"
        stroke-opacity="0.08"
        stroke-dasharray="3 3"
      />
      <text
        x={xScale(tick)}
        y={HEIGHT - 5}
        text-anchor="middle"
        font-size="9"
        fill="currentColor"
        fill-opacity="0.5"
      >
        {config.format(tick)}
      </text>
    {/each}

    {#each chartData as row, i (row.id)}
      {@const barTop = yBands.start(i)}
      <rect
        x={PAD.left}
        y={barTop}
        width={Math.max(1, xScale(row[viewMode]) - PAD.left)}
        height={yBands.bandWidth}
        rx="3"
        fill={BAR_COLOR}
        fill-opacity="0.85"
      >
        <title>{row.id}: {config.format(row[viewMode])}</title>
      </rect>
      <text
        x={PAD.left - 8}
        y={barTop + yBands.bandWidth / 2 + 3}
        text-anchor="end"
        font-size="10"
        fill="currentColor"
        fill-opacity="0.7"
      >
        {row.label}
      </text>
    {/each}

    <line
      x1={PAD.left}
      x2={PAD.left}
      y1={PAD.top}
      y2={PAD.top + plotH}
      stroke="currentColor"
      stroke-opacity="0.2"
    />
  </svg>
</ChartFrame>
