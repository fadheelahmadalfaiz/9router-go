<script lang="ts">
  // Time-series chart for tokens / requests / cost, ported from upstream
  // UsageChart.js (recharts AreaChart). Hand-rolled SVG: the LayerChart port
  // mounted and drew its axes but never emitted the area path, so the card
  // rendered empty. See scale.ts for why.
  import { api, type UsageChartPoint } from '../../../api/client'
  import type { Period } from '../types'
  import ChartFrame from './ChartFrame.svelte'
  import { fmtCost, fmtRequests, fmtTokens } from './formatters'
  import { bandScale, linePath, linearScale, niceMax, niceTicks } from './scale'

  interface Props {
    period: Period
  }

  let { period }: Props = $props()

  type ViewMode = 'tokens' | 'requests' | 'cost'

  const VIEW_MODES: { value: ViewMode; label: string }[] = [
    { value: 'tokens', label: 'Tokens' },
    { value: 'requests', label: 'Requests' },
    { value: 'cost', label: 'Cost' },
  ]

  const VIEW_CONFIG: Record<ViewMode, { color: string; format: (n: number) => string; label: string }> = {
    tokens: { color: '#6366f1', format: fmtTokens, label: 'Tokens' },
    requests: { color: '#14b8a6', format: fmtRequests, label: 'Requests' },
    cost: { color: '#f59e0b', format: fmtCost, label: 'Cost' },
  }

  const HEIGHT = 220
  const PAD = { top: 8, right: 10, bottom: 22, left: 46 }
  const LABEL_EVERY = 7

  let viewMode = $state<ViewMode>('tokens')
  let data = $state<UsageChartPoint[]>([])
  let loading = $state(false)
  let lastLoadedPeriod = $state<string | null>(null)

  const config = $derived(VIEW_CONFIG[viewMode])
  const hasData = $derived(data.some((point) => (point[viewMode] || 0) > 0))

  // Width is fixed rather than measured: a ResizeObserver-driven chart that
  // starts at 0 and never re-measures is exactly the class of bug this port
  // already shipped once, and these cards live in a fluid grid where a fixed
  // viewBox scales cleanly.
  const VIEW_W = 720
  const plotW = VIEW_W - PAD.left - PAD.right
  const plotH = HEIGHT - PAD.top - PAD.bottom

  const maxValue = $derived(niceMax(Math.max(0, ...data.map((p) => p[viewMode] || 0))))
  const yScale = $derived(linearScale([0, maxValue], [PAD.top + plotH, PAD.top]))
  const xBands = $derived(bandScale(data.length, [PAD.left, PAD.left + plotW], 0.2))
  const yTicks = $derived(niceTicks(maxValue, 5))

  const points = $derived(
    data.map((p, i) => ({ x: xBands.center(i), y: yScale(p[viewMode] || 0) })),
  )
  const areaPath = $derived.by(() => {
    if (points.length === 0) return ''
    const base = PAD.top + plotH
    return `${linePath(points)} L ${points[points.length - 1].x.toFixed(2)} ${base} L ${points[0].x.toFixed(2)} ${base} Z`
  })

  // Only a few x labels fit; a tick per point would overlap into noise.
  const labelStep = $derived(Math.max(1, Math.ceil(data.length / LABEL_EVERY)))

  $effect(() => {
    const target = period
    if (lastLoadedPeriod === target) return
    lastLoadedPeriod = target
    loading = true
    api
      .getUsageChart(target)
      .then((points) => {
        data = points || []
      })
      .catch((error) => {
        console.error('Failed to fetch usage chart:', error)
        data = []
      })
      .finally(() => {
        loading = false
      })
  })
</script>

<ChartFrame {loading} empty={!loading && !hasData} height={HEIGHT}>
  {#snippet controls()}
    <div class="grid grid-cols-3 items-center gap-1 rounded-lg border border-border bg-surface-2 p-1">
      {#each VIEW_MODES as mode (mode.value)}
        <button
          type="button"
          onclick={() => (viewMode = mode.value)}
          class="cursor-pointer rounded-md px-3 py-1 text-sm font-medium transition-colors {viewMode ===
          mode.value
            ? 'bg-primary text-white shadow-sm'
            : 'text-text-muted hover:bg-surface-3 hover:text-text-main'}"
        >
          {mode.label}
        </button>
      {/each}
    </div>
  {/snippet}

  <svg
    viewBox="0 0 {VIEW_W} {HEIGHT}"
    class="h-full w-full"
    preserveAspectRatio="none"
    role="img"
    aria-label="{config.label} over time"
  >
    <defs>
      <linearGradient id="usage-area-grad" x1="0" y1="0" x2="0" y2="1">
        <stop offset="5%" stop-color={config.color} stop-opacity="0.28" />
        <stop offset="95%" stop-color={config.color} stop-opacity="0" />
      </linearGradient>
    </defs>

    <!-- gridlines + y labels -->
    {#each yTicks as tick (tick)}
      <line
        x1={PAD.left}
        x2={PAD.left + plotW}
        y1={yScale(tick)}
        y2={yScale(tick)}
        stroke="currentColor"
        stroke-opacity="0.1"
        stroke-dasharray="3 3"
      />
      <text
        x={PAD.left - 8}
        y={yScale(tick) + 3}
        text-anchor="end"
        font-size="10"
        fill="currentColor"
        fill-opacity="0.55"
      >
        {config.format(tick)}
      </text>
    {/each}

    <!-- x labels -->
    {#each data as point, i (point.label + i)}
      {#if i % labelStep === 0}
        <text
          x={xBands.center(i)}
          y={HEIGHT - 6}
          text-anchor="middle"
          font-size="10"
          fill="currentColor"
          fill-opacity="0.55"
        >
          {point.label}
        </text>
      {/if}
    {/each}

    <!-- baseline -->
    <line
      x1={PAD.left}
      x2={PAD.left + plotW}
      y1={PAD.top + plotH}
      y2={PAD.top + plotH}
      stroke="currentColor"
      stroke-opacity="0.2"
    />

    {#if hasData}
      <path d={areaPath} fill="url(#usage-area-grad)" stroke="none" />
      <path
        d={linePath(points)}
        fill="none"
        stroke={config.color}
        stroke-width="2"
        stroke-linejoin="round"
        stroke-linecap="round"
      />
    {/if}
  </svg>
</ChartFrame>
