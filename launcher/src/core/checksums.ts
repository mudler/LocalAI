// Reads a checksum list, shared by the desktop installer and a client-only
// shell. Pure, no Electron or Node import: hashing the file stays with each
// side.

/** Parse `sha256sum` output: "<64 hex>  <name>" or "<64 hex> *<name>". */
export function parseChecksums(text: string): Map<string, string> {
  const out = new Map<string, string>()
  for (const raw of text.split(/\r?\n/)) {
    const m = /^([0-9a-fA-F]{64})\s+\*?(.+?)\s*$/.exec(raw)
    if (m) out.set(m[2], m[1].toLowerCase())
  }
  return out
}
