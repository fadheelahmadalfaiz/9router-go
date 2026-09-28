// Provider list derivation for the usage topology. Ported from upstream
// src/shared/components/UsageStats.js (isLLMProvider + noAuth free providers).
//
// The previous implementation hardcoded a five-entry FREE_DEFAULTS array and
// added every active connection unfiltered, so media-only providers (TTS, STT,
// image) rendered as LLM nodes and a newly catalogued no-auth provider stayed
// invisible until someone hand-edited the array. Both are now derived from the
// catalog's own noAuth / serviceKinds flags.
import { PROVIDER_CATALOG, type ProviderCatalogItem } from '../../lib/providers'
import type { ActiveRequestItem, RecentRequestItem } from './types'

/** The slice of a provider connection the topology needs. */
export interface TopologyConnection {
  id: string
  provider?: string
  isActive?: number | boolean
  name?: string | null
}

// Upstream: providers without serviceKinds default to LLM, otherwise the list
// must include "llm". Catalog entries carry the same field.
export function isLLMProvider(providerId: string): boolean {
  if (!providerId) return false
  const id = providerId.toLowerCase().trim()
  const item =
    PROVIDER_CATALOG.find((p) => p.id.toLowerCase() === id) ||
    PROVIDER_CATALOG.find((p) => p.alias?.toLowerCase() === id)
  if (!item?.serviceKinds) return true
  return item.serviceKinds.includes('llm')
}

export function getCatalogItem(providerId: string): ProviderCatalogItem | undefined {
  if (!providerId) return undefined
  const id = providerId.toLowerCase().trim()
  return (
    PROVIDER_CATALOG.find((p) => p.id.toLowerCase() === id) ||
    PROVIDER_CATALOG.find((p) => p.alias?.toLowerCase() === id)
  )
}

// No-auth, LLM-capable providers that are always drawn even with no connection,
// so the topology is not empty on a fresh install.
export function getFreeLLMProviderIds(): string[] {
  return PROVIDER_CATALOG.filter((p) => p.noAuth && isLLMProvider(p.id)).map((p) => p.id)
}

export interface TopologyProvider {
  id: string
  alias?: string
  name: string
  color?: string
  type: 'active' | 'recent' | 'error' | 'connection' | 'stats' | 'default'
}

export interface TopologyInput {
  connections: TopologyConnection[]
  providerNodes: { id: string; name?: string | null }[]
  activeRequests: ActiveRequestItem[]
  recentRequests: RecentRequestItem[]
  pulseProvider?: string
  lastProvider?: string
  errorProvider?: string
  byProvider?: Record<string, unknown>
}

const DEFAULT_COLOR = '#3B82F6'

function nodeNameById(nodes: { id: string; name?: string | null }[]): Map<string, string> {
  const map = new Map<string, string>()
  for (const node of nodes || []) {
    if (node?.id && node?.name) map.set(node.id, node.name)
  }
  return map
}

function resolveName(providerId: string, nodeNames: Map<string, string>, customName?: string): string {
  const nodeName = nodeNames.get(providerId)
  if (nodeName) return nodeName
  const item = getCatalogItem(providerId)
  if (item?.name) return item.name
  if (customName && customName !== providerId) {
    // Numeric key names (e.g. "12") are connection labels, not provider names,
    // so a custom node must never render as "12".
    if (!/^\d+$/.test(customName.trim())) return customName
  }
  return providerId
}

// Order matters: providers seen on live traffic first, so a line to a model that
// is actually in use is never dropped by the cap on inactive entries.
export function buildTopologyProviders(input: TopologyInput): TopologyProvider[] {
  const nodeNames = nodeNameById(input.providerNodes)
  const seen = new Set<string>()
  const list: TopologyProvider[] = []

  const add = (providerId: string, type: TopologyProvider['type'], customName?: string): void => {
    if (!providerId || !isLLMProvider(providerId)) return
    const canonical = providerId.toLowerCase().trim()
    const item = getCatalogItem(canonical)
    const targetId = item?.id || canonical
    if (seen.has(targetId)) return
    seen.add(targetId)
    if (item?.alias) seen.add(item.alias.toLowerCase())
    seen.add(canonical)

    list.push({
      id: targetId,
      alias: item?.alias,
      name: resolveName(targetId, nodeNames, customName),
      color: item?.color || DEFAULT_COLOR,
      type,
    })
  }

  for (const request of input.activeRequests || []) {
    if (request.provider) add(request.provider, 'active')
  }
  if (input.pulseProvider) add(input.pulseProvider, 'active')
  if (input.lastProvider) add(input.lastProvider, 'recent')
  if (input.errorProvider) add(input.errorProvider, 'error')

  for (const request of input.recentRequests || []) {
    if (request.provider) add(request.provider, 'recent')
  }

  for (const connection of input.connections || []) {
    if (connection.isActive !== 0 && connection.provider) {
      add(connection.provider, 'connection', connection.name || undefined)
    }
  }

  for (const providerId of Object.keys(input.byProvider || {})) add(providerId, 'stats')

  for (const freeId of getFreeLLMProviderIds()) add(freeId, 'default')

  return list
}
