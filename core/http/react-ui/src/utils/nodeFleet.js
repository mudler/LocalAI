const HEALTH_STATUSES = ['healthy', 'unhealthy', 'offline', 'pending', 'draining']

function finiteNumber(value) {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

function clamp(value, minimum, maximum) {
  return Math.min(maximum, Math.max(minimum, value))
}

export function capacityReading(totalValue, availableValue) {
  const total = finiteNumber(totalValue)
  const reportedAvailable = finiteNumber(availableValue)
  if (total === null || total <= 0 || reportedAvailable === null) return null

  const available = clamp(reportedAvailable, 0, total)
  const used = total - available
  return { total, used, available, usagePercent: (used / total) * 100 }
}

export function nodeLifecycleAction(status) {
  if (status === 'healthy') return 'drain'
  if (status === 'draining') return 'resume'
  if (status === 'pending') return 'approve'
  return null
}

function capacitySummary(nodes, totalField, availableField) {
  let total = 0
  let available = 0
  let reportingCount = 0

  for (const node of nodes) {
    const reading = capacityReading(node?.[totalField], node?.[availableField])
    if (!reading) continue

    total += reading.total
    available += reading.available
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

function hasLowCapacity(node, totalField, availableField) {
  const reading = capacityReading(node?.[totalField], node?.[availableField])
  return reading !== null && reading.available / reading.total <= 0.1
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

function modelTimestamp(value) {
  if (typeof value !== 'string' || value.trim() === '') return null
  const timestamp = Date.parse(value)
  return Number.isFinite(timestamp) ? timestamp : null
}

export function groupModels(input) {
  const rows = Array.isArray(input) ? input : []
  const groups = new Map()

  for (const replica of rows) {
    const modelName = typeof replica?.model_name === 'string' ? replica.model_name.trim() : ''
    if (!modelName) continue
    if (!groups.has(modelName)) {
      groups.set(modelName, {
        model_name: modelName,
        replicas: [],
        replica_count: 0,
        node_count: 0,
        in_flight: 0,
        backend_types: [],
        last_used: null,
      })
    }

    const group = groups.get(modelName)
    group.replicas.push(replica)
    group.replica_count += 1
    const inFlight = finiteNumber(replica.in_flight)
    group.in_flight += inFlight === null ? 0 : Math.max(0, Math.floor(inFlight))
  }

  for (const group of groups.values()) {
    group.node_count = new Set(group.replicas.map(replica => String(replica?.node_id ?? '').trim()).filter(Boolean)).size
    group.backend_types = [...new Set(group.replicas.map(replica => String(replica?.backend_type ?? '').trim()).filter(Boolean))]
      .sort((left, right) => compareValues(left, right))
    const mostRecent = group.replicas.reduce((latest, replica) => {
      const timestamp = modelTimestamp(replica?.last_used)
      return timestamp !== null && (latest === null || timestamp > latest.timestamp)
        ? { timestamp, value: replica.last_used }
        : latest
    }, null)
    group.last_used = mostRecent?.value ?? null
  }

  return [...groups.values()]
}

export function filterModels(input, query = '') {
  const models = Array.isArray(input) ? input : []
  const normalizedQuery = String(query ?? '').trim().toLowerCase()
  if (!normalizedQuery) return [...models]
  return models.filter(model => [model?.model_name, ...(Array.isArray(model?.backend_types) ? model.backend_types : [])]
    .some(value => String(value ?? '').toLowerCase().includes(normalizedQuery)))
}

export function sortModels(input, sort = {}) {
  const models = Array.isArray(input) ? input : []
  const key = sort.key || 'model_name'
  const direction = sort.direction === 'desc' ? -1 : 1

  return models
    .map((model, index) => ({ model, index }))
    .sort((left, right) => {
      let primary
      if (key === 'last_used') {
        const leftTime = modelTimestamp(left.model?.last_used)
        const rightTime = modelTimestamp(right.model?.last_used)
        if (leftTime === null || rightTime === null) primary = leftTime === rightTime ? 0 : leftTime === null ? 1 : -1
        else primary = (leftTime - rightTime) * direction
      } else {
        primary = compareValues(left.model?.[key], right.model?.[key]) * direction
      }
      if (primary !== 0) return primary
      const byName = compareValues(left.model?.model_name, right.model?.model_name)
      return byName || left.index - right.index
    })
    .map(entry => entry.model)
}

export function paginateModels(input, requestedPage = 1) {
  return paginateNodes(input, requestedPage, 50)
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
