import { describe, expect, it } from 'bun:test'
import { fmtCost, fmtRequests, fmtTokens, truncateLabel } from './formatters'

describe('fmtTokens', () => {
  const cases: { name: string; input: number | undefined; expected: string }[] = [
    { name: 'formats millions', input: 2_500_000, expected: '2.5M' },
    { name: 'formats thousands', input: 12_345, expected: '12.3K' },
    { name: 'leaves small values alone', input: 999, expected: '999' },
    { name: 'treats missing as zero', input: undefined, expected: '0' },
  ]

  for (const testCase of cases) {
    it(testCase.name, () => {
      expect(fmtTokens(testCase.input)).toBe(testCase.expected)
    })
  }
})

describe('fmtCost', () => {
  it('keeps four decimals, matching the upstream chart axis', () => {
    expect(fmtCost(1.23456)).toBe('$1.2346')
  })

  it('treats missing as zero', () => {
    expect(fmtCost(undefined)).toBe('$0.0000')
  })
})

describe('fmtRequests', () => {
  it('renders the raw count', () => {
    expect(fmtRequests(42)).toBe('42')
  })
})

describe('truncateLabel', () => {
  it('leaves a short label untouched', () => {
    expect(truncateLabel('gpt-5', 22)).toBe('gpt-5')
  })

  it('truncates with an ellipsis character', () => {
    expect(truncateLabel('a-very-long-model-name-here', 10)).toBe('a-very-lon…')
  })

  it('handles an empty label', () => {
    expect(truncateLabel('', 10)).toBe('')
  })
})
