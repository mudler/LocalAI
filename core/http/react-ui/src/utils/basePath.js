// The server injects a path-only <base href> ("/" or "/prefix/"). Resolve it
// against the page address: new URL() without a base throws on a bare path.
export function basePathFromHref(href, pageHref) {
  if (!href) return ''
  try {
    return new URL(href, pageHref).pathname.replace(/\/+$/, '')
  } catch { return '' }
}

function getBasePath() {
  if (typeof document === 'undefined') return ''
  const el = document.querySelector('base[href]')
  if (!el) return ''
  return basePathFromHref(el.getAttribute('href'), window.location.href)
}

export const basePath = getBasePath()
export const routerBasename = basePath || '/'

export function apiUrl(path) {
  if (!basePath) return path
  if (path.startsWith('http://') || path.startsWith('https://')) return path
  return basePath + path
}
