// The version string the server sends is "<version> (<commit>)". A build made
// without the linker flags has neither, and the string is then " ()": nothing
// worth showing. This returns '' for that, and the text trimmed otherwise.
export function displayVersion(raw) {
  const text = String(raw == null ? '' : raw).trim()
  if (/^\(\s*\)$/.test(text)) return ''
  return text.replace(/\s*\(\s*\)\s*$/, '').trim()
}
