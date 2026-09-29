import { describe, expect, it } from 'bun:test'
import {
  DEFAULT_SWEEP_LABELS,
  emptySweepOutcome,
  formatSweepOutcome,
  runSweep,
  type SweepOutcome,
} from './modelSweep'

function outcome(partial: Partial<SweepOutcome>): SweepOutcome {
  return { ...emptySweepOutcome(), ...partial }
}

describe('runSweep', () => {
  it('probes every item in order and counts the outcomes', async () => {
    const seen: string[] = []
    const controller = new AbortController()

    const result = await runSweep(
      ['a', 'b', 'c'],
      async (item) => {
        seen.push(item)
        return item !== 'b'
      },
      controller.signal,
    )

    expect(seen).toEqual(['a', 'b', 'c'])
    expect(result).toEqual({ total: 3, tested: 3, passed: 2, failed: 1, cancelled: false })
  })

  it('stops on an already-aborted signal without probing anything', async () => {
    const controller = new AbortController()
    controller.abort()
    let calls = 0

    const result = await runSweep(
      ['a', 'b'],
      async () => {
        calls++
        return true
      },
      controller.signal,
    )

    expect(calls).toBe(0)
    expect(result).toEqual({ total: 2, tested: 0, passed: 0, failed: 0, cancelled: true })
  })

  it('stops after the item that cancels it, and keeps the earlier results', async () => {
    const controller = new AbortController()
    const seen: string[] = []

    const result = await runSweep(
      ['a', 'b', 'c', 'd'],
      async (item) => {
        seen.push(item)
        // The user gives up while the third model is on the wire.
        if (item === 'c') controller.abort()
        return true
      },
      controller.signal,
    )

    // c was aborted mid-flight, so it is neither passed nor failed.
    expect(seen).toEqual(['a', 'b', 'c'])
    expect(result).toEqual({ total: 4, tested: 2, passed: 2, failed: 0, cancelled: true })
  })

  it('does not count a probe that rejects because of the cancel as a failure', async () => {
    const controller = new AbortController()

    const result = await runSweep(
      ['a', 'b'],
      async (item) => {
        if (item === 'b') {
          controller.abort()
          throw new DOMException('aborted', 'AbortError')
        }
        return true
      },
      controller.signal,
    )

    expect(result.cancelled).toBe(true)
    expect(result.failed).toBe(0)
    expect(result.passed).toBe(1)
  })

  it('propagates a real failure so the caller can surface it', async () => {
    const controller = new AbortController()
    await expect(
      runSweep(
        ['a'],
        async () => {
          throw new Error('boom')
        },
        controller.signal,
      ),
    ).rejects.toThrow('boom')
  })

  it('reports an empty list as nothing to do rather than a pass', async () => {
    const controller = new AbortController()
    const result = await runSweep([], async () => true, controller.signal)
    expect(result).toEqual({ total: 0, tested: 0, passed: 0, failed: 0, cancelled: false })
  })
})

describe('formatSweepOutcome', () => {
  it('reports a clean run', () => {
    expect(formatSweepOutcome(outcome({ total: 3, tested: 3, passed: 3 }))).toBe('3/3 models reachable')
  })

  it('appends the failure count', () => {
    expect(formatSweepOutcome(outcome({ total: 4, tested: 4, passed: 3, failed: 1 }))).toBe(
      '3/4 models reachable · 1 failed',
    )
  })

  it('says how far a cancelled run got instead of implying a full pass', () => {
    expect(
      formatSweepOutcome(outcome({ total: 40, tested: 12, passed: 11, failed: 1, cancelled: true })),
    ).toBe('Stopped · 12 of 40 tested · 11 passed')
  })

  it('never reports a cancelled run as fully reachable', () => {
    const text = formatSweepOutcome(
      outcome({ total: 40, tested: 40, passed: 40, cancelled: true }),
    )
    expect(text).toContain('Stopped')
  })

  it('reports an empty list plainly', () => {
    expect(formatSweepOutcome(emptySweepOutcome())).toBe(DEFAULT_SWEEP_LABELS.none)
  })

  it('honours custom labels', () => {
    expect(
      formatSweepOutcome(outcome({ total: 2, tested: 2, passed: 2 }), {
        ...DEFAULT_SWEEP_LABELS,
        done: '{total} ok',
      }),
    ).toBe('2 ok')
  })
})
