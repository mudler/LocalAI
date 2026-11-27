import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'
import { buildAddonUI } from '../../internal/uibuild/uibuild.mjs'

const here = dirname(fileURLToPath(import.meta.url))

export async function build() {
  // Compat mode, like Talk: the page imports the host modules by their usual
  // names and addons/internal/host-compat answers them through the SDK. It also
  // bundles the sheet, the transcript and the style sheet of the Talk add-on.
  return buildAddonUI({ root: here, name: 'talking-head', rgb: [116, 72, 140], glyph: 'head', compat: resolve(here, '../../internal/host-compat/src') })
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  build().then((dir) => console.log(`built ${dir}`), (e) => { console.error(e); process.exit(1) })
}
