import { fileURLToPath } from 'node:url'
import { dirname } from 'node:path'
import { buildAddonUI } from '../../internal/uibuild/uibuild.mjs'

const here = dirname(fileURLToPath(import.meta.url))

export async function build() {
  return buildAddonUI({ root: here, name: 'voice', rgb: [58, 38, 120] })
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  build().then((dir) => console.log(`built ${dir}`), (e) => { console.error(e); process.exit(1) })
}
