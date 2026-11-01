// The links a page asks to open. A page runs in a sandbox that cannot open a
// window or navigate the app, so a click on <a href> or a window.open() in the
// page becomes a bridge call link.open(url), and the host asks the person
// before it opens anything. Only a plain http or https address goes through.
// No React and no network here.

export const MAX_LINK = 2048

// pageLinkTarget checks a URL that a page wants to open. It resolves to
// {href, host, display, site, lookalike} or {error}:
//
//   href       the normalised URL, the one that opens
//   host       the host in ASCII (punycode), as the browser sends it
//   display    the host with its punycode labels decoded, for the warning
//   site       the last two labels of host: the part to look at
//   lookalike  the host has punycode labels, so it may imitate another site
//
// A relative URL, //host, another scheme (javascript:, data:, file:, blob:),
// credentials in the URL, a missing host and an address over MAX_LINK are
// refused. Nothing resolves against the page or the app.
export function pageLinkTarget(raw) {
  if (typeof raw !== 'string') return { error: 'not_a_url' }
  const s = raw.trim()
  if (s === '' || s.length > MAX_LINK) return { error: 'not_a_url' }
  if (!/^https?:\/\//i.test(s)) return { error: /^[a-z][a-z0-9+.-]*:/i.test(s) ? 'scheme' : 'relative' }
  let u
  try {
    u = new URL(s)
  } catch {
    return { error: 'not_a_url' }
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return { error: 'scheme' }
  if (u.username || u.password) return { error: 'credentials' }
  const host = u.hostname.replace(/\.$/, '').toLowerCase()
  if (!host) return { error: 'not_a_url' }
  const labels = host.split('.')
  const lookalike = labels.some((l) => l.startsWith('xn--'))
  const display = lookalike ? labels.map(decodeLabel).join('.') : host
  const site = /^[\d.]+$/.test(host) || host.startsWith('[') ? host : labels.slice(-2).join('.')
  return { href: u.href, host, display, site, lookalike }
}

// The messages a page gets back for a refused link, by error.
export const LINK_ERRORS = {
  not_a_url: 'the link is not a valid address',
  scheme: 'only http and https links open from a page',
  relative: 'a link to a file of the page opens nothing: a page has no other pages; use an http or https address',
  credentials: 'a link with a user name or a password in it does not open',
}

function decodeLabel(label) {
  if (!label.startsWith('xn--')) return label
  try {
    return punycodeDecode(label.slice(4))
  } catch {
    return label
  }
}

// RFC 3492 decoding of one label, without the xn-- prefix.
export function punycodeDecode(input) {
  const base = 36
  const tMin = 1
  const tMax = 26
  const skew = 38
  const damp = 700
  const adapt = (delta, numPoints, first) => {
    let d = first ? Math.floor(delta / damp) : delta >> 1
    d += Math.floor(d / numPoints)
    let k = 0
    while (d > ((base - tMin) * tMax) >> 1) {
      d = Math.floor(d / (base - tMin))
      k += base
    }
    return k + Math.floor(((base - tMin + 1) * d) / (d + skew))
  }
  const digit = (c) => {
    if (c >= 48 && c <= 57) return c - 22
    if (c >= 65 && c <= 90) return c - 65
    if (c >= 97 && c <= 122) return c - 97
    throw new Error('bad punycode')
  }
  const out = []
  const end = input.lastIndexOf('-')
  for (let j = 0; j < Math.max(end, 0); j++) out.push(input.charCodeAt(j))
  let n = 128
  let bias = 72
  let i = 0
  for (let pos = end > 0 ? end + 1 : 0; pos < input.length;) {
    const oldi = i
    let w = 1
    for (let k = base; ; k += base) {
      if (pos >= input.length) throw new Error('bad punycode')
      const d = digit(input.charCodeAt(pos++))
      i += d * w
      const t = k <= bias ? tMin : k >= bias + tMax ? tMax : k - bias
      if (d < t) break
      w *= base - t
    }
    bias = adapt(i - oldi, out.length + 1, oldi === 0)
    n += Math.floor(i / (out.length + 1))
    i %= out.length + 1
    if (n > 0x10ffff) throw new Error('bad punycode')
    out.splice(i++, 0, n)
  }
  return String.fromCodePoint(...out)
}
