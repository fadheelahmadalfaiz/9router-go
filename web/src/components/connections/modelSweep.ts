// Cancellable sequential sweep used by the two "Test All Models" buttons on the
// provider detail page. Kept out of the component so the cancellation and
// partial-result accounting are testable without a render harness.

export interface SweepOutcome {
  /** How many items the sweep started with. */
  total: number
  /** How many were actually probed before it stopped. */
  tested: number
  passed: number
  failed: number
  /** True when the user stopped it rather than letting it finish. */
  cancelled: boolean
}

export interface SweepLabels {
  done: string
  withFailures: string
  cancelled: string
  none: string
}

export const DEFAULT_SWEEP_LABELS: SweepLabels = {
  done: '{passed}/{total} models reachable',
  withFailures: '{passed}/{total} models reachable · {failed} failed',
  cancelled: 'Stopped · {tested} of {total} tested · {passed} passed',
  none: 'No models to test',
}

export function emptySweepOutcome(total = 0): SweepOutcome {
  return { total, tested: 0, passed: 0, failed: 0, cancelled: false }
}

export function fillTemplate(template: string, values: Record<string, string | number>): string {
  return template.replace(/\{(\w+)\}/g, (match, key: string) =>
    key in values ? String(values[key]) : match,
  )
}

export function formatSweepOutcome(
  outcome: SweepOutcome,
  labels: SweepLabels = DEFAULT_SWEEP_LABELS,
): string {
  if (outcome.total === 0) return labels.none
  const values = {
    total: outcome.total,
    tested: outcome.tested,
    passed: outcome.passed,
    failed: outcome.failed,
  }
  if (outcome.cancelled) return fillTemplate(labels.cancelled, values)
  if (outcome.failed > 0) return fillTemplate(labels.withFailures, values)
  return fillTemplate(labels.done, values)
}

/**
 * Probes each item in order, stopping as soon as `signal` aborts.
 *
 * Sequential on purpose: every model here is reached through the same account,
 * and firing them all at once is the fastest way to earn a rate limit on the
 * very account being tested. The in-flight probe is aborted too, so a cancel
 * does not have to wait out the request that is already on the wire.
 *
 * `testOne` receives the item and the signal; it resolves true for a pass.
 * Whatever the sweep already confirmed stays confirmed — a partial run reports
 * what it actually probed rather than pretending it covered the list.
 */
export async function runSweep(
  items: string[],
  testOne: (item: string, signal: AbortSignal) => Promise<boolean>,
  signal: AbortSignal,
): Promise<SweepOutcome> {
  const outcome = emptySweepOutcome(items.length)

  for (const item of items) {
    if (signal.aborted) {
      outcome.cancelled = true
      break
    }

    let passed = false
    try {
      passed = await testOne(item, signal)
    } catch (error) {
      if (signal.aborted) {
        // The probe rejected because we cancelled it, not because the model is
        // broken. Counting it as a failure would be a lie.
        outcome.cancelled = true
        break
      }
      throw error
    }

    // A model that never got probed (aborted mid-flight) is not a failure.
    if (signal.aborted) {
      outcome.cancelled = true
      break
    }

    outcome.tested++
    if (passed) outcome.passed++
    else outcome.failed++
  }

  if (signal.aborted) outcome.cancelled = true
  return outcome
}
