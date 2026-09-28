<script lang="ts">
  // Time-series chart for tokens / requests / cost, ported from upstream
  // UsageChart.js (recharts AreaChart). LayerChart's AreaChart is the Svelte 5
  // equivalent and renders SVG, so the gradient, axes and tooltip config map
  // across one-for-one.
  import { AreaChart, Axis, Rule, Tooltip } from 'layerchart/svg'
  import { api, type UsageChartPoint } from '../../../api/client'
  import type { Period } from '../types'
  import ChartFrame from './ChartFrame.svelte'
  import { fmtCost, fmtRequests, fmtTokens } from './formatters'

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

  const VIEW_CONFIG: Record<
    ViewMode,
    { color: string; gradientId: string; format: (n: number) => string; label: string }
  > = {
    tokens: {
      color: '#6366f1',
      gradientId: 'usage-grad-tokens',
      format: fmtTokens,
      label: 'Tokens',
    },
    requests: {
      color: '#14b8a6',
      gradientId: 'usage-grad-requests',
      format: fmtRequests,
      label: 'Requests',
    },
    cost: {
      color: '#f59e0b',
      gradientId: 'usage-grad-cost',
      format: fmtCost,
      label: 'Cost',
    },
  }

  let viewMode = $state<ViewMode>('tokens')
  let data = $state<UsageChartPoint[]>([])
  let loading = $state(false)
  let lastLoadedPeriod = $state<string | null>(null)

  const config = $derived(VIEW_CONFIG[viewMode])
  const hasData = $derived(data.some((point) => (point[viewMode] || 0) > 0))
  const series = $derived([
    {
      key: viewMode,
      value: (point: UsageChartPoint) => point[viewMode] || 0,
      color: config.color,
      props: {
        fill: `url(#${config.gradientId})`,
        stroke: config.color,
        line: { strokeWidth: 2 },
      },
    },
  ])

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

<ChartFrame {loading} empty={!loading && !hasData} height={220}>
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

  <AreaChart
    {data}
    x="label"
    {series}
    yDomain={[0, null]}
    yNice
    padding={{ top: 4, right: 8, bottom: 0, left: 0 }}
    height={220}
    tooltipContext={{ x: 'data' }}
  >
    <defs>
      {#each VIEW_MODES as mode (mode.value)}
        <linearGradient id={VIEW_CONFIG[mode.value].gradientId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="5%" stop-color={VIEW_CONFIG[mode.value].color} stop-opacity="0.25" />
          <stop offset="95%" stop-color={VIEW_CONFIG[mode.value].color} stop-opacity="0" />
        </linearGradient>
      {/each}
    </defs>

    <Rule y={0} />

    <Axis
      placement="left"
      grid={{ stroke: 'currentColor', opacity: 0.1, dashArray: [3, 3] }}
      format={config.format}
      rule={false}
      tickMarks={false}
      tickLabelProps={{ fontSize: 10, fill: 'currentColor', fillOpacity: 0.5 }}
    />
    <Axis
      placement="bottom"
      rule={false}
      tickMarks={false}
      tickLabelProps={{ fontSize: 10, fill: 'currentColor', fillOpacity: 0.5 }}
    />
  </AreaChart>

  <Tooltip.Root>
    {#snippet children({ data: hovered })}
      {#if hovered}
        <Tooltip.Header>
          {hovered.label}
        </Tooltip.Header>
        <Tooltip.List>
          <Tooltip.Item label={config.label} color={config.color} format={config.format}>
            {hovered[viewMode]}
          </Tooltip.Item>
        </Tooltip.List>
      {/if}
    {/snippet}
  </Tooltip.Root>
</ChartFrame>
