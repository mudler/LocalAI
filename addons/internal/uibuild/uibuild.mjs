// Builds the UI package files of a first-party add-on, laid out as the
// package root expects them: dist/ui/<name>.js (one ES module), dist/ui/<name>.css
// and dist/ui/icon.png. It uses the bundler the host UI already has installed
// (core/http/react-ui/node_modules), compiles JSX to calls of `h`, and never
// bundles React: the host's React arrives through the SDK. The same method as
// addons/tables/ui/build.mjs, for add-ons that share this builder.
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { dirname, join, resolve, sep } from 'node:path'
import { copyFileSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { deflateSync } from 'node:zlib'

const here = dirname(fileURLToPath(import.meta.url))
const hostUi = resolve(here, '../../../core/http/react-ui')
const require = createRequire(join(hostUi, 'package.json'))

// A solid rounded square, 128 by 128, written by hand so a package build needs
// no image tool and no download.
function solidIcon([r, g, b]) {
  const size = 128
  const row = size * 4 + 1
  const px = Buffer.alloc(size * row)
  const radius = 22
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const cx = Math.min(Math.max(x, radius), size - 1 - radius)
      const cy = Math.min(Math.max(y, radius), size - 1 - radius)
      const inside = (x - cx) ** 2 + (y - cy) ** 2 <= radius * radius
      const o = y * row + 1 + x * 4
      px[o] = r; px[o + 1] = g; px[o + 2] = b; px[o + 3] = inside ? 255 : 0
    }
  }
  const table = Array.from({ length: 256 }, (_, n) => { let c = n; for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1; return c >>> 0 })
  const crc = (buf) => { let c = 0xffffffff; for (const byte of buf) c = table[(c ^ byte) & 0xff] ^ (c >>> 8); return (c ^ 0xffffffff) >>> 0 }
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

// Fails the build when a module of the host UI tree (src/ or node_modules/)
// is pulled into the bundle: the add-on uses the host's shared components
// through the SDK, never a second copy.
function hostGuard() {
  const prefix = hostUi + sep
  return {
    name: 'addon-host-guard',
    load(id) {
      if (id.startsWith(prefix)) throw new Error(`the bundle must not include the host UI file ${id}; use the SDK instead`)
      return null
    },
  }
}

const reactImport = /(?:\bfrom\s*|\bimport\s*\(?\s*|\brequire\s*\(\s*)(['"])(react|react-dom)(?:\/[^'"]*)?\1/

export async function buildAddonUI({ root, name, rgb }) {
  const out = join(root, 'dist')
  const { build: bundle } = await import(require.resolve('rolldown'))
  rmSync(out, { recursive: true, force: true })
  mkdirSync(join(out, 'ui'), { recursive: true })
  await bundle({
    input: join(root, 'src/index.jsx'),
    // React stays a visible import so the check below can see it. The host
    // gives the add-on React through the SDK, so any such import is refused.
    external: [/^react(-dom)?(\/|$)/],
    plugins: [hostGuard()],
    output: { file: join(out, `ui/${name}.js`), format: 'esm', minify: false, sourcemap: false },
    transform: { jsx: { runtime: 'classic', pragma: 'h', pragmaFrag: 'Fragment' } },
    logLevel: 'warn',
  })
  copyFileSync(join(root, 'src/style.css'), join(out, `ui/${name}.css`))
  writeFileSync(join(out, 'ui/icon.png'), solidIcon(rgb))
  const js = readFileSync(join(out, `ui/${name}.js`), 'utf8')
  const bad = reactImport.exec(js)
  if (bad) throw new Error(`the bundle must not import React: found ${bad[0]}`)
  if (/\brequire\(/.test(js)) throw new Error('the bundle must not call require()')
  return out
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const [root, name, r, g, b] = process.argv.slice(2)
  buildAddonUI({ root: resolve(root), name, rgb: [Number(r), Number(g), Number(b)] })
    .then((dir) => console.log(`built ${dir}`), (e) => { console.error(e); process.exit(1) })
}
