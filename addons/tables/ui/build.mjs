// Builds the Tables UI package files, laid out as the package root expects
// them: dist/ui/tables.js (one ES module), dist/ui/tables.css and
// dist/ui/icon.png. The manifest names exactly these paths. It uses the bundler the host UI already
// has installed (core/http/react-ui/node_modules) so that a package build
// needs nothing to download, and it compiles JSX to React.createElement
// calls on the `h` of each factory: there is no JSX runtime import and no
// React in the output (the host's React arrives through the SDK).
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve } from 'node:path'
import { mkdirSync, rmSync, writeFileSync, copyFileSync, readFileSync } from 'node:fs'
import { deflateSync } from 'node:zlib'

const here = dirname(fileURLToPath(import.meta.url))
const hostUi = resolve(here, '../../../core/http/react-ui')
const require = createRequire(join(hostUi, 'package.json'))
const out = join(here, 'dist')

// A small PNG written by hand: a rounded square in the host accent colors
// with a three-by-three grid, 128 by 128, so the package carries a real icon
// with no image tool and no download.
function icon() {
  const size = 128
  const px = Buffer.alloc(size * (size * 4 + 1))
  const put = (x, y, r, g, b, a = 255) => {
    const o = y * (size * 4 + 1) + 1 + x * 4
    px[o] = r; px[o + 1] = g; px[o + 2] = b; px[o + 3] = a
  }
  const inside = (x, y, r) => {
    const cx = Math.min(Math.max(x, r), size - 1 - r)
    const cy = Math.min(Math.max(y, r), size - 1 - r)
    return (x - cx) ** 2 + (y - cy) ** 2 <= r * r
  }
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      if (!inside(x, y, 22)) { put(x, y, 0, 0, 0, 0); continue }
      put(x, y, 26, 62, 138)
    }
  }
  // The grid: header band, then rows of cells, in a light tone.
  const cell = 20
  const gap = 6
  const left = (size - (3 * cell + 2 * gap)) / 2
  for (let r = 0; r < 3; r++) {
    for (let c = 0; c < 3; c++) {
      const x0 = left + c * (cell + gap)
      const y0 = left + r * (cell + gap)
      const head = r === 0
      for (let y = y0; y < y0 + cell; y++) {
        for (let x = x0; x < x0 + cell; x++) head ? put(x, y, 120, 220, 190) : put(x, y, 226, 234, 250)
      }
    }
  }
  const crcTable = Array.from({ length: 256 }, (_, n) => { let c = n; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; return c >>> 0 })
  const crc = (buf) => { let c = 0xffffffff; for (const b of buf) c = crcTable[(c ^ b) & 0xff] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0 }
  const chunk = (type, data) => {
    const len = Buffer.alloc(4); len.writeUInt32BE(data.length)
    const body = Buffer.concat([Buffer.from(type), data])
    const sum = Buffer.alloc(4); sum.writeUInt32BE(crc(body))
    return Buffer.concat([len, body, sum])
  }
  const head = Buffer.alloc(13)
  head.writeUInt32BE(size, 0); head.writeUInt32BE(size, 4)
  head[8] = 8; head[9] = 6
  return Buffer.concat([Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]), chunk('IHDR', head), chunk('IDAT', deflateSync(px, { level: 9 })), chunk('IEND', Buffer.alloc(0))])
}

export async function build() {
  const { build: bundle } = await import(require.resolve('rolldown'))
  rmSync(out, { recursive: true, force: true })
  mkdirSync(join(out, 'ui'), { recursive: true })
  await bundle({
    input: join(here, 'src/index.jsx'),
    // The host's React is never bundled: nothing here imports it.
    external: [],
    output: { file: join(out, 'ui/tables.js'), format: 'esm', minify: false, sourcemap: false },
    transform: { jsx: { runtime: 'classic', pragma: 'h', pragmaFrag: 'Fragment' } },
    logLevel: 'warn',
  })
  copyFileSync(join(here, 'src/style.css'), join(out, 'ui/tables.css'))
  writeFileSync(join(out, 'ui/icon.png'), icon())
  const js = readFileSync(join(out, 'ui/tables.js'), 'utf8')
  if (/from\s+['"](react|react-dom)/.test(js) || /require\(/.test(js)) throw new Error('the bundle must not import React')
  return out
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  build().then((dir) => console.log(`built ${dir}`), (e) => { console.error(e); process.exit(1) })
}
