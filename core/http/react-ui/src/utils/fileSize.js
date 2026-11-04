const MIB = 1024 * 1024

// A size as the connector sentences write it: bytes, KiB, MiB, GiB or TiB.
export function formatFileSize(n) {
  if (n === 1) return '1 byte'
  if (n < 1024) return `${n} bytes`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${Number(v.toFixed(1))} ${units[i]}`
}

// The ceiling is typed in MiB and stored in bytes. Empty means the default.
export const bytesToMib = (bytes) => (bytes > 0 ? String(bytes / MIB) : '')
export function mibToBytes(text) {
  const t = String(text ?? '').trim()
  const n = Number(t)
  return t === '' || !Number.isFinite(n) ? 0 : Math.round(n * MIB)
}
