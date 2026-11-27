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
