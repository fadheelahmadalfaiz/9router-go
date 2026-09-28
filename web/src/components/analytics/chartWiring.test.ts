import { describe, expect, it } from 'bun:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

const ANALYTICS_VIEW = join(import.meta.dir, 'AnalyticsView.svelte')
const source = readFileSync(ANALYTICS_VIEW, 'utf8')

// The charts used to be behind a lazy `import()` gated on a nullable holder
// component. A chart that disappears with no error message, no console output
// the user notices, and no layout shift is the worst possible failure mode, and
// it is invisible to every other kind of test in this repo. A dashboard served
// from localhost gains nothing meaningful from the split, so the charts are
// imported statically and rendered unconditionally; these tests keep it that
// way.
describe('AnalyticsView chart wiring', () => {
  const components = ['UsageChart', 'ProviderBarChart', 'TopModelsChart']

  for (const component of components) {
    it(`imports ${component} statically`, () => {
      expect(source).toMatch(new RegExp(`import\\s+${component}\\s+from\\s+'\\./charts/${component}\\.svelte'`))
    })

    it(`renders ${component} directly`, () => {
      expect(source).toContain(`<${component}`)
    })
  }

  it('does not lazy-import the chart components', () => {
    for (const component of components) {
      expect(source).not.toMatch(new RegExp(`import\\(\\s*'\\./charts/${component}\\.svelte'`))
    }
  })

  it('does not gate the chart block behind a nullable module holder', () => {
    // A `{#if charts}`-style guard turns any load failure into a silently
    // absent section, so the marker must not come back.
    expect(source).not.toMatch(/\{#if\s+charts\}/)
    expect(source).not.toMatch(/<charts\./)
  })
})
