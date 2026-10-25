import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'
import { buildAddonUI } from '../../internal/uibuild/uibuild.mjs'

const here = dirname(fileURLToPath(import.meta.url))

export async function build() {
  // The page code keeps the host module names it was written with; the compat
  // directory answers them through the SDK (addons/internal/host-compat).
  return buildAddonUI({ root: here, name: 'talk', rgb: [28, 112, 104], compat: resolve(here, '../../internal/host-compat/src') })
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  build().then((dir) => console.log(`built ${dir}`), (e) => { console.error(e); process.exit(1) })
}
