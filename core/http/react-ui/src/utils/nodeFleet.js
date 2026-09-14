const HEALTH_STATUSES = ['healthy', 'unhealthy', 'offline', 'pending', 'draining']

function finiteNumber(value) {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function clamp(value, minimum, maximum) {
  return Math.min(maximum, Math.max(minimum, value))
}

function capacitySummary(nodes, totalField, availableField) {
  let total = 0
  let available = 0
  let reportingCount = 0

  for (const node of nodes) {
    const nodeTotal = finiteNumber(node?.[totalField])
    if (nodeTotal === null || nodeTotal <= 0) continue

    const reportedAvailable = finiteNumber(node?.[availableField]) ?? 0
    total += nodeTotal
    available += clamp(reportedAvailable, 0, nodeTotal)
    reportingCount += 1
  }

  const used = total - available
  return {
    total,
    used,
    available,
    usagePercent: total > 0 ? (used / total) * 100 : 0,
    reportingCount,
    unknownCount: nodes.length - reportingCount,
  }
}

function cpuSummary(nodes) {
  let totalLogicalCores = 0
  let busyCoreEquivalents = 0
  let load1 = 0
  let reportingCount = 0

  for (const node of nodes) {
    const logicalCores = finiteNumber(node?.cpu_logical_cores)
    const usage = finiteNumber(node?.cpu_usage_percent)
    const reportedLoad = finiteNumber(node?.cpu_load_1)
    if (logicalCores === null || logicalCores <= 0 || usage === null || reportedLoad === null) continue

    const usagePercent = clamp(usage, 0, 100)
    totalLogicalCores += logicalCores
    busyCoreEquivalents += logicalCores * usagePercent / 100
    load1 += Math.max(0, reportedLoad)
    reportingCount += 1
  }

  return {
    totalLogicalCores,
    busyCoreEquivalents,
    idleCoreEquivalents: totalLogicalCores - busyCoreEquivalents,
    usagePercent: totalLogicalCores > 0 ? (busyCoreEquivalents / totalLogicalCores) * 100 : 0,
    load1,
    reportingCount,
    unknownCount: nodes.length - reportingCount,
  }
}

function reportsCapacity(node, totalField) {
  const total = finiteNumber(node?.[totalField])
  return total !== null && total > 0
}

function hasLowCapacity(node, totalField, availableField) {
  if (!reportsCapacity(node, totalField)) return false
  const total = node[totalField]
  const reportedAvailable = finiteNumber(node?.[availableField])
  if (reportedAvailable === null) return false
  const available = clamp(reportedAvailable, 0, total)
  return available / total <= 0.1
}

export function summarizeFleet(input) {
  const nodes = Array.isArray(input) ? input : []
  const health = {
    total: nodes.length,
    healthy: 0,
    draining: 0,
    pending: 0,
    offline: 0,
    unhealthy: 0,
    other: 0,
  }

  const attention = {
    pending: [],
    offlineOrUnhealthy: [],
    lowVRAM: [],
    lowRAM: [],
    lowDisk: [],
  }

  nodes.forEach(node => {
    const status = typeof node?.status === 'string' ? node.status.toLowerCase() : ''
    if (HEALTH_STATUSES.includes(status)) health[status] += 1
    else health.other += 1

    if (status === 'pending') attention.pending.push(node?.id)
    if (status === 'offline' || status === 'unhealthy') attention.offlineOrUnhealthy.push(node?.id)
    if (hasLowCapacity(node, 'total_vram', 'available_vram')) attention.lowVRAM.push(node?.id)
    if (hasLowCapacity(node, 'total_ram', 'available_ram')) attention.lowRAM.push(node?.id)
    if (hasLowCapacity(node, 'total_disk', 'available_disk')) attention.lowDisk.push(node?.id)
  })

  const attentionIds = new Set(Object.values(attention).flat())

  return {
    health,
    attentionNodeCount: attentionIds.size,
    attention,
    vram: capacitySummary(nodes, 'total_vram', 'available_vram'),
    ram: capacitySummary(nodes, 'total_ram', 'available_ram'),
    cpu: cpuSummary(nodes),
    disk: capacitySummary(nodes, 'total_disk', 'available_disk'),
  }
}

function normalizedSet(values) {
  if (!values) return new Set()
  const iterable = values instanceof Set || Array.isArray(values) ? values : [values]
  return new Set([...iterable].map(value => String(value).toLowerCase()))
}

function searchableValues(node) {
  const labels = node?.labels && typeof node.labels === 'object' && !Array.isArray(node.labels)
    ? Object.entries(node.labels).flat()
    : []
  return [
    node?.name,
    node?.address,
    node?.node_type,
    node?.status,
    node?.model_count,
    node?.gpu_vendor,
    node?.capability,
    ...labels,
  ]
}

export function filterNodes(input, filters = {}) {
  const nodes = Array.isArray(input) ? input : []
  const query = String(filters.query ?? '').trim().toLowerCase()
  const statuses = normalizedSet(filters.statuses)
  const types = normalizedSet(filters.types)

  return nodes.filter(node => {
    const status = String(node?.status ?? '').toLowerCase()
    const type = String(node?.node_type ?? '').toLowerCase()
    if (statuses.size > 0 && !statuses.has(status)) return false
    if (types.size > 0 && !types.has(type)) return false
    if (!query) return true
    return searchableValues(node).some(value => String(value ?? '').toLowerCase().includes(query))
  })
}

function compareValues(left, right) {
  const leftMissing = left == null || (typeof left === 'number' && !Number.isFinite(left))
  const rightMissing = right == null || (typeof right === 'number' && !Number.isFinite(right))
  if (leftMissing || rightMissing) return leftMissing === rightMissing ? 0 : leftMissing ? 1 : -1

  const leftNumber = finiteNumber(left)
  const rightNumber = finiteNumber(right)
  if (leftNumber !== null && rightNumber !== null) return leftNumber - rightNumber
  return String(left).localeCompare(String(right), undefined, { numeric: true, sensitivity: 'base' })
}

export function sortNodes(input, sort = {}) {
  const nodes = Array.isArray(input) ? input : []
  const key = sort.key || 'name'
  const direction = sort.direction === 'desc' ? -1 : 1

  return nodes
    .map((node, index) => ({ node, index }))
    .sort((left, right) => {
      const primary = compareValues(left.node?.[key], right.node?.[key]) * direction
      if (primary !== 0) return primary
      const byName = compareValues(left.node?.name, right.node?.name)
      return byName || left.index - right.index
    })
    .map(entry => entry.node)
}

function groupDescriptor(node, groupBy) {
  if (groupBy === 'node_type') {
    const value = node?.node_type
    if (value == null || String(value).trim() === '') return { key: 'node-type:missing', label: 'Unlabelled', missing: true }
    return { key: `node-type:value:${JSON.stringify(String(value))}`, label: String(value), missing: false }
  }

  if (typeof groupBy === 'string' && groupBy.startsWith('label:')) {
    const labelKey = groupBy.slice('label:'.length)
    const labels = node?.labels && typeof node.labels === 'object' && !Array.isArray(node.labels) ? node.labels : {}
    const value = Object.hasOwn(labels, labelKey) ? labels[labelKey] : undefined
    if (value == null || String(value).trim() === '') return { key: 'label:missing', label: 'Unlabelled', missing: true }
    return { key: `label:value:${JSON.stringify(String(value))}`, label: String(value), missing: false }
  }

  return null
}

export function groupNodes(input, groupBy = 'none') {
  const nodes = Array.isArray(input) ? input : []
  if (groupBy === 'none' || !groupBy) return [{ key: 'all', label: 'All nodes', nodes: [...nodes] }]

  const groups = new Map()
  for (const node of nodes) {
    const descriptor = groupDescriptor(node, groupBy)
    if (!descriptor) return [{ key: 'all', label: 'All nodes', nodes: [...nodes] }]
    if (!groups.has(descriptor.key)) groups.set(descriptor.key, { ...descriptor, nodes: [] })
    groups.get(descriptor.key).nodes.push(node)
  }

  return [...groups.values()]
    .sort((left, right) => {
      if (left.missing !== right.missing) return left.missing ? 1 : -1
      return compareValues(left.label, right.label)
    })
    .map(({ key, label, nodes: groupedNodes }) => ({ key, label, nodes: groupedNodes }))
}

export function paginateNodes(input, requestedPage = 1, requestedPageSize = 50) {
  const nodes = Array.isArray(input) ? input : []
  const numericPageSize = Number.isFinite(requestedPageSize) ? Math.floor(requestedPageSize) : 50
  const pageSize = numericPageSize > 0 ? numericPageSize : 50
  const totalItems = nodes.length
  const totalPages = Math.max(1, Math.ceil(totalItems / pageSize))
  const numericPage = Number.isFinite(Number(requestedPage)) ? Math.floor(Number(requestedPage)) : 1
  const page = clamp(numericPage, 1, totalPages)
  const start = (page - 1) * pageSize

  return {
    page,
    pageSize,
    totalItems,
    totalPages,
    items: nodes.slice(start, start + pageSize),
  }
}

export async function runBounded(input, limit, operation) {
  const items = Array.isArray(input) ? input : []
  if (!Number.isInteger(limit) || limit <= 0) throw new RangeError('limit must be a positive integer')
  if (typeof operation !== 'function') throw new TypeError('operation must be a function')

  const results = new Array(items.length)
  let nextIndex = 0

  async function worker() {
    while (nextIndex < items.length) {
      const index = nextIndex
      nextIndex += 1
      try {
        results[index] = { status: 'fulfilled', value: await operation(items[index], index) }
      } catch (reason) {
        results[index] = { status: 'rejected', reason }
      }
    }
  }

  const workers = Array.from({ length: Math.min(limit, items.length) }, () => worker())
  await Promise.all(workers)
  return results
}
