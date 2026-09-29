// Minimal linear-scale helpers for the hand-rolled usage charts.
//
// These three charts were originally ported onto LayerChart 2.5, which mounted
// its container and drew its axes but never emitted the mark geometry: the
// <Area> path and the <Bar> rects were absent, so every card rendered as an
// empty box. Rather than debug a bleeding-edge charting library, the charts
// draw their own SVG — the same approach ProviderTopologyCard already uses in
// this dashboard, which renders correctly. That also drops the ~264 kB gzip
// layerchart added to every dashboard page.

export interface LinearScale {
  (value: number): number
  invert: (pixel: number) => number
  domain: [number, number]
  range: [number, number]
  ticks: number
}

/** Maps a data domain onto a pixel range. A zero-width range is pinned to 0. */
export function linearScale(
  domain: [number, number],
  range: [number, number],
  tickCount = 5,
): LinearScale {
  const [d0, d1] = domain
  const [r0, r1] = range
  const span = d1 - d0

  const scale = ((value: number) => {
    if (span === 0) return r0
    const ratio = (value - d0) / span
    return r0 + ratio * (r1 - r0)
  }) as LinearScale

  scale.invert = (pixel: number) => {
    if (r1 === r0) return d0
    return d0 + ((pixel - r0) / (r1 - r0)) * span
  }
  scale.domain = [d0, d1]
  scale.range = [r0, r1]
  scale.ticks = tickCount
  return scale
}

/**
 * Tick values for a linear domain, "nice"-rounded the way an axis does so
 * labels read as round numbers instead of raw data extremes.
 */
export function niceTicks(max: number, count = 5): number[] {
  if (!Number.isFinite(max) || max <= 0) return [0]
  const rough = max / Math.max(1, count)
  const magnitude = 10 ** Math.floor(Math.log10(rough))
  const normalized = rough / magnitude
  const step =
    (normalized <= 1 ? 1 : normalized <= 2 ? 2 : normalized <= 5 ? 5 : 10) * magnitude
  const ticks: number[] = []
  for (let v = 0; v <= max + step * 0.001; v += step) {
    ticks.push(Math.round(v * 1e6) / 1e6)
  }
  return ticks.length > 1 ? ticks : [0, max]
}

/** The axis maximum that covers every value, rounded up to a nice step. */
export function niceMax(max: number, count = 5): number {
  if (!Number.isFinite(max) || max <= 0) return 1
  const rough = max / Math.max(1, count)
  const magnitude = 10 ** Math.floor(Math.log10(rough))
  const normalized = rough / magnitude
  const step =
    (normalized <= 1 ? 1 : normalized <= 2 ? 2 : normalized <= 5 ? 5 : 10) * magnitude
  return Math.ceil(max / step) * step
}

/** Band positions for a categorical axis. */
export function bandScale(count: number, range: [number, number], padding = 0.25) {
  const [r0, r1] = range
  const width = count > 0 ? (r1 - r0) / count : 0
  const step = width / (1 + padding)
  const bandWidth = Math.max(1, width - step)
  return {
    width,
    bandWidth,
    center: (i: number) => r0 + width * i + width / 2,
    start: (i: number) => r0 + width * i + step / 2,
  }
}

/** Straight-segment path through the given points. */
export function linePath(points: { x: number; y: number }[]): string {
  if (points.length === 0) return ''
  return points
    .map((p, i) => `${i === 0 ? 'M' : 'L'} ${p.x.toFixed(2)} ${p.y.toFixed(2)}`)
    .join(' ')
}
