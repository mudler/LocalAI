import { formatBytes } from './format.js'

// The states the prompt cache janitor reports (pkg/promptcache, State), with
// the tone and the words of the Settings page.
export const PROMPT_CACHE_STATES = {
  on: { tone: 'ok', label: 'On' },
  paused_low_disk: { tone: 'warn', label: 'Paused: low disk' },
  disabled_disk_full: { tone: 'error', label: 'Off: disk almost full' },
  unknown: { tone: 'idle', label: 'Disk not read yet' },
}

export function promptCacheState(st) {
  return PROMPT_CACHE_STATES[st?.state] || PROMPT_CACHE_STATES.unknown
}

// Why the tier is off on one node (promptcachesvc, the Off constants), in the
// words of the Settings page.
export const PROMPT_CACHE_OFF_REASONS = {
  no_path: 'Off: no cache directory',
  switched_off: 'Off',
  network_disk: 'Off: network disk',
  not_writable: 'Off: not writable',
}

// The chip label of a node with the tier off.
export function promptCacheOffLabel(reason) {
  return PROMPT_CACHE_OFF_REASONS[reason] || 'Off'
}

// The one-line message of a node with the tier off for a reason the operator
// can fix, else null.
export function promptCacheOffMessage(reason) {
  if (reason === 'network_disk') {
    return 'Off: the cache directory is on a network disk. Set a directory on a local disk with --prompt-cache-path or LOCALAI_PROMPT_CACHE_PATH on this node.'
  }
  if (reason === 'not_writable') {
    return 'Off: the cache directory is not writable. Set a writable directory on a local disk with --prompt-cache-path or LOCALAI_PROMPT_CACHE_PATH on this node.'
  }
  if (reason === 'no_path') {
    return 'Off: this node has no cache directory. Set --data-path or --prompt-cache-path on it.'
  }
  return null
}

// The one-line message of a state that needs attention, else null.
export function promptCacheMessage(st) {
  if (!st) return null
  if (st.state === 'paused_low_disk') {
    return `The disk has ${formatBytes(st.free_bytes)} free, under the low mark of ${formatBytes(st.low_watermark_bytes)}. The cache writes nothing new and deletes its oldest entries until the disk has that much free again.`
  }
  if (st.state === 'disabled_disk_full') {
    return `The disk has ${formatBytes(st.free_bytes)} free, under the critical mark of ${formatBytes(st.critical_watermark_bytes)}. The cache is off until the free space is above ${formatBytes(st.low_watermark_bytes)} again.`
  }
  return null
}

// The one-line total of a report with several nodes (distributed mode), else
// null. It counts the nodes that answered and says how many did not.
export function promptCacheTotal(data) {
  const nodes = data?.nodes || []
  const t = data?.total
  if (nodes.length < 2 || !t) return null
  let line = `${formatBytes(t.bytes || 0)} in ${t.files || 0} ${t.files === 1 ? 'entry' : 'entries'} on ${t.nodes || 0} ${t.nodes === 1 ? 'node' : 'nodes'}`
  if (t.cap_bytes > 0) line += `, of at most ${formatBytes(t.cap_bytes)}`
  line += '.'
  if (t.unreachable > 0) line += ` ${t.unreachable} ${t.unreachable === 1 ? 'node did' : 'nodes did'} not answer.`
  return line
}
