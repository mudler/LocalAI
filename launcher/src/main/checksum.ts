// Checksum verification that fails closed. No Electron import.
import { createHash } from 'node:crypto'
import fs from 'node:fs'

export class ChecksumError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'ChecksumError'
  }
}

/** Parse `sha256sum` output: "<64 hex>  <name>" or "<64 hex> *<name>". */
export function parseChecksums(text: string): Map<string, string> {
  const out = new Map<string, string>()
  for (const raw of text.split(/\r?\n/)) {
    const m = /^([0-9a-fA-F]{64})\s+\*?(.+?)\s*$/.exec(raw)
    if (m) out.set(m[2], m[1].toLowerCase())
  }
  return out
}

export function sha256File(file: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const h = createHash('sha256')
    fs.createReadStream(file)
      .on('data', (d) => h.update(d))
      .on('error', reject)
      .on('end', () => resolve(h.digest('hex')))
  })
}

/**
 * Verify `file` against the checksum list. Anything short of a match throws:
 * no checksum text, no entry for the asset, a malformed list, or a different hash.
 */
export async function verifyChecksum(file: string, assetName: string, checksumText: string | undefined): Promise<void> {
  if (!checksumText || checksumText.trim() === '') throw new ChecksumError('no checksum file; refusing to install')
  const sums = parseChecksums(checksumText)
  if (sums.size === 0) throw new ChecksumError('checksum file has no valid entries; refusing to install')
  const want = sums.get(assetName)
  if (!want) throw new ChecksumError(`no checksum listed for ${assetName}; refusing to install`)
  const got = await sha256File(file)
  if (got !== want) throw new ChecksumError(`checksum mismatch for ${assetName}`)
}

/**
 * SHA-512 of a file as base64, the form electron-updater's feed files use.
 * Synchronous: it runs once, on "Restart to update", in a step that must
 * finish before the rename.
 */
export function sha512Base64Sync(file: string): string {
  const h = createHash('sha512')
  const fd = fs.openSync(file, 'r')
  try {
    const buf = Buffer.alloc(1 << 20)
    for (;;) {
      const n = fs.readSync(fd, buf, 0, buf.length, null)
      if (n === 0) break
      h.update(buf.subarray(0, n))
    }
  } finally {
    fs.closeSync(fd)
  }
  return h.digest('base64')
}
