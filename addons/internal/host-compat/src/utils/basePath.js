// The path prefix LocalAI is served under, read the way the host reads it
// (core utils/basePath.js): from the <base href> element. Unlike the host file
// it is safe to import where there is no document.
function getBasePath() {
  if (typeof document === 'undefined') return ''
  const el = document.querySelector('base[href]')
  if (!el) return ''
  try {
    return new URL(el.getAttribute('href')).pathname.replace(/\/+$/, '')
  } catch { return '' }
}

export const basePath = getBasePath()
export const routerBasename = basePath || '/'

export function apiUrl(path) {
  if (!basePath) return path
  if (path.startsWith('http://') || path.startsWith('https://')) return path
  return basePath + path
}
