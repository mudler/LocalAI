import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'
import { buildAddonUI } from '../../internal/uibuild/uibuild.mjs'

const here = dirname(fileURLToPath(import.meta.url))
buildAddonUI({ root: here, name: 'google-team', rgb: [45, 110, 180], compat: resolve(here, '../../internal/host-compat/src') })
  .then((dir) => console.log(`built ${dir}`), (err) => { console.error(err); process.exit(1) })
