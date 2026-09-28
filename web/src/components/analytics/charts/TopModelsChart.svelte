<script lang="ts">
  // Top-5 models by tokens/requests, ported from upstream TopModelsChart.js
  // (recharts BarChart with layout="vertical" — the horizontal orientation of
  // LayerChart's BarChart).
  import { Axis, BarChart, Tooltip } from 'layerchart/svg'
  import type { UsageItem } from '../types'
  import ChartFrame from './ChartFrame.svelte'
  import { fmtRequests, fmtTokens, truncateLabel } from './formatters'

  interface Props {
    byModel?: Record<string, UsageItem>
  }

  let { byModel = {} }: Props = $props()

  type ViewMode = 'tokens' | 'requests'

  const BAR_COLOR = '#6366f1'
  const LABEL_MAX = 22
  const TOP_N = 5

  const VIEW_CONFIG: Record<ViewMode, { format: (n: number) => string; label: string }> = {
    tokens: { format: fmtTokens, label: 'Tokens' },
    requests: { format: fmtRequests, label: 'Requests' },
  }

  let viewMode = $state<ViewMode>('tokens')

  const config = $derived(VIEW_CONFIG[viewMode])

  const chartData = $derived(
    Object.values(byModel || {})
      .map((item) => ({
        name: truncateLabel(item.rawModel || 'Unknown', LABEL_MAX),
        fullName: item.rawModel || 'Unknown',
        tokens: (item.promptTokens || 0) + (item.completionTokens || 0),
        requests: item.requests || 0,
      }))
      .filter((row) => row[viewMode] > 0)
      .sort((a, b) => b[viewMode] - a[viewMode])
      .slice(0, TOP_N),
  )

  // Upstream tints each bar from a palette via recharts <Cell>. LayerChart has
  // no per-datum fill prop without a categorical color scale, and d3-scale is
  // only a transitive dependency here, so the bars share one colour instead.
  const series = $derived([
    {
      key: viewMode,
      value: (row: (typeof chartData)[number]) => row[viewMode],
      color: BAR_COLOR,
      props: { radius: 4 },
    },
  ])
</script>

<ChartFrame title="Top Models" empty={chartData.length === 0} emptyMessage="No model usage yet" height={180}>
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
          {VIEW_CONFIG[mode].label}
        </button>
      {/each}
    </div>
  {/snippet}

  <BarChart
    data={chartData}
    x="name"
    {series}
    orientation="horizontal"
    yDomain={[0, null]}
    yNice
    padding={{ top: 4, right: 40, left: 4, bottom: 4 }}
    height={180}
    bandPadding={0.3}
    tooltipContext={{ x: 'data' }}
  >
    <Axis
      placement="bottom"
      grid={{ stroke: 'currentColor', opacity: 0.1, dashArray: [3, 3] }}
      format={config.format}
      rule={false}
      tickMarks={false}
      tickLabelProps={{ fontSize: 10, fill: 'currentColor', fillOpacity: 0.5 }}
    />
    <Axis
      placement="left"
      rule={false}
      tickMarks={false}
      tickLabelProps={{ fontSize: 10, fill: 'currentColor', fillOpacity: 0.7 }}
    />
  </BarChart>

  <Tooltip.Root>
    {#snippet children({ data: hovered })}
      {#if hovered}
        <Tooltip.Header>{hovered.fullName}</Tooltip.Header>
        <Tooltip.List>
          <Tooltip.Item label={config.label} format={config.format}>
            {hovered[viewMode]}
          </Tooltip.Item>
        </Tooltip.List>
      {/if}
    {/snippet}
  </Tooltip.Root>
</ChartFrame>
