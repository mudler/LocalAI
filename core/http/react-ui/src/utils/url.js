// safeHref returns the input URL only if its scheme is on a small allowlist
// (http, https, mailto, tel) or if it's a relative/anchor link. Anything else
// — most importantly `javascript:` and `data:` — collapses to '#'. Use this
// for any <a href={...}> whose URL comes from gallery JSON, agent tool calls,
// or any other source the operator hasn't fully vetted.
//
// React already escapes attribute values, so the only XSS path on a hyperlink
// is the URI itself. javascript: in <img src> is inert in modern browsers,
// but <a href="javascript:..."> still fires on click.
const ALLOWED_SCHEMES = ['http:', 'https:', 'mailto:', 'tel:']

export function safeHref(url) {
  if (typeof url !== 'string' || url === '') return '#'
  const trimmed = url.trim()
  if (trimmed === '') return '#'
  // Relative paths, fragment links, and protocol-relative URLs are fine.
  if (trimmed.startsWith('/') || trimmed.startsWith('#') || trimmed.startsWith('?')) {
    return trimmed
  }
  if (trimmed.startsWith('//')) return trimmed
  // Heuristic: if there's no colon before the first slash, it's a relative path.
  const colonIdx = trimmed.indexOf(':')
  if (colonIdx === -1) return trimmed
  const slashIdx = trimmed.indexOf('/')
  if (slashIdx !== -1 && slashIdx < colonIdx) return trimmed
  // There is a scheme — allowlist-check it. Browsers ignore tabs/newlines
  // inside the scheme (`java\tscript:...`), so we strip control chars first.
  const scheme = trimmed.slice(0, colonIdx).toLowerCase().replace(/[\x00-\x1f]/g, '')
  if (ALLOWED_SCHEMES.includes(scheme + ':')) return trimmed
  return '#'
}
