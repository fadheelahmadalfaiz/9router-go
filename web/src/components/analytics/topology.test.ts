import { describe, expect, it } from 'bun:test'
import { PROVIDER_CATALOG } from '../../lib/providers'
import {
  buildTopologyProviders,
  getCatalogItem,
  getFreeLLMProviderIds,
  isLLMProvider,
  type TopologyInput,
} from './topology'

const baseInput: TopologyInput = {
  connections: [],
  providerNodes: [],
  activeRequests: [],
  recentRequests: [],
  byProvider: {},
}

describe('isLLMProvider', () => {
  it('accepts an LLM provider', () => {
    expect(isLLMProvider('claude')).toBe(true)
  })

  it('rejects a TTS-only provider', () => {
    expect(isLLMProvider('elevenlabs')).toBe(false)
  })

  it('rejects an STT-only provider', () => {
    expect(isLLMProvider('assemblyai')).toBe(false)
  })

  it('rejects a web-search-only provider', () => {
    expect(isLLMProvider('searxng')).toBe(false)
  })

  it('treats a mixed provider as LLM', () => {
    expect(isLLMProvider('opencode')).toBe(true)
  })

  it('resolves through the alias', () => {
    const aliased = PROVIDER_CATALOG.find((p) => p.alias && p.alias !== p.id && !isLLMProvider(p.id))
    if (!aliased) return
    expect(isLLMProvider(aliased.alias)).toBe(false)
  })

  it('rejects an empty id', () => {
    expect(isLLMProvider('')).toBe(false)
  })

  it('defaults to true for a provider absent from the catalog', () => {
    expect(isLLMProvider('some-custom-node')).toBe(true)
  })
})

describe('getFreeLLMProviderIds', () => {
  const free = getFreeLLMProviderIds()

  it('includes a known no-auth LLM provider', () => {
    expect(free).toContain('opencode')
  })

  it('excludes no-auth providers that cannot serve chat', () => {
    // These are the exact rows the old hardcoded list dodged by accident: they
    // are noAuth, so a naive noAuth filter would put TTS/search engines in a
    // chat topology.
    for (const id of ['coqui', 'edge-tts', 'google-tts', 'tortoise', 'searxng', 'local-device']) {
      expect(free).not.toContain(id)
    }
  })

  it('only returns catalog entries flagged noAuth that are also LLM', () => {
    for (const id of free) {
      const item = getCatalogItem(id)
      expect(item).toBeDefined()
      expect(item!.noAuth).toBe(true)
      expect(isLLMProvider(id)).toBe(true)
    }
  })

  it('has no duplicates', () => {
    expect(new Set(free).size).toBe(free.length)
  })
})

describe('buildTopologyProviders', () => {
  it('excludes a media-only connection', () => {
    const providers = buildTopologyProviders({
      ...baseInput,
      connections: [
        { id: 'c1', provider: 'elevenlabs', isActive: 1 },
        { id: 'c2', provider: 'claude', isActive: 1 },
      ],
    })
    const ids = providers.map((p) => p.id)
    expect(ids).toContain('claude')
    expect(ids).not.toContain('elevenlabs')
  })

  it('excludes a disabled connection', () => {
    const providers = buildTopologyProviders({
      ...baseInput,
      connections: [{ id: 'c1', provider: 'claude', isActive: 0 }],
    })
    expect(providers.map((p) => p.id)).not.toContain('claude')
  })

  it('prefers live traffic over idle connections', () => {
    const providers = buildTopologyProviders({
      ...baseInput,
      connections: [{ id: 'c1', provider: 'kiro', isActive: 1 }],
      activeRequests: [{ provider: 'codex' }],
    })
    expect(providers[0].id).toBe('codex')
  })

  it('deduplicates by canonical id across every source', () => {
    const providers = buildTopologyProviders({
      ...baseInput,
      activeRequests: [{ provider: 'claude' }],
      recentRequests: [{ provider: 'claude' }],
      connections: [{ id: 'c1', provider: 'claude', isActive: 1 }],
      byProvider: { claude: {} },
    })
    expect(providers.filter((p) => p.id === 'claude')).toHaveLength(1)
  })

  it('names a custom node from the provider-node list', () => {
    const providers = buildTopologyProviders({
      ...baseInput,
      providerNodes: [{ id: 'kiro', name: 'My Kiro' }],
      connections: [{ id: 'c1', provider: 'kiro', isActive: 1 }],
    })
    expect(providers.find((p) => p.id === 'kiro')?.name).toBe('My Kiro')
  })

  it('never renders a numeric connection label as a provider name', () => {
    const providers = buildTopologyProviders({
      ...baseInput,
      connections: [{ id: 'c1', provider: 'my-custom-node', name: '12', isActive: 1 }],
    })
    expect(providers.find((p) => p.id === 'my-custom-node')?.name).toBe('my-custom-node')
  })

  it('always seeds the no-auth LLM defaults on an empty install', () => {
    const providers = buildTopologyProviders(baseInput)
    const ids = providers.map((p) => p.id)
    for (const id of getFreeLLMProviderIds()) {
      expect(ids).toContain(id)
    }
  })

  it('drops a media-only provider even when it is the last provider seen', () => {
    const providers = buildTopologyProviders({
      ...baseInput,
      lastProvider: 'elevenlabs',
      errorProvider: 'assemblyai',
    })
    const ids = providers.map((p) => p.id)
    expect(ids).not.toContain('elevenlabs')
    expect(ids).not.toContain('assemblyai')
  })
})
