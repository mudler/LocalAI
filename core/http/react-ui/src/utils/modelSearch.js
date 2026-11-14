// Helpers shared by the online sources of the Explore tab.

// 1234 -> "1.2k", 3400000 -> "3.4M". Small numbers stay as they are.
export function compactCount(n) {
  if (!Number.isFinite(n) || n <= 0) return ''
  if (n < 1000) return String(Math.round(n))
  const units = [[1e9, 'B'], [1e6, 'M'], [1e3, 'k']]
  for (const [size, suffix] of units) {
    if (n >= size) return `${parseFloat((n / size).toFixed(1))}${suffix}`
  }
  return String(n)
}
