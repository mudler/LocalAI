function getBasePath() {
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
