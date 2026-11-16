import { fileURLToPath } from 'node:url'
import { dirname } from 'node:path'
import { appendFileSync, readFileSync } from 'node:fs'
import { buildAddonUI } from '../../internal/uibuild/uibuild.mjs'
export const build = async () => {
  const out = await buildAddonUI({ root: dirname(fileURLToPath(import.meta.url)), name: 'asset-pipeline', rgb: [34, 94, 128] })
  const license = readFileSync(new URL('./node_modules/three/LICENSE', import.meta.url), 'utf8')
  appendFileSync(new URL('./dist/ui/asset-pipeline.js', import.meta.url), `\n/* Third-party license: Three.js\n${license}\n*/\n`)
  return out
}
if (process.argv[1] === fileURLToPath(import.meta.url)) build().then(console.log).catch(error => { console.error(error); process.exit(1) })
