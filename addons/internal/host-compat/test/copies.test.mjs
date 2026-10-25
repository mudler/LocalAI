import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

// The add-on builder refuses a bundle that imports a core UI file, so these
// files are copies. They must stay byte-equal to the core originals.
const here = dirname(fileURLToPath(import.meta.url))
const compat = join(here, '../src')
const core = resolve(here, '../../../../core/http/react-ui/src')
const read = (p) => readFileSync(p, 'utf8')

const COPIES = [
  'utils/format.js', 'utils/capabilities.js', 'utils/modelLedger.js', 'utils/modelBudget.js', 'utils/mcpClientStorage.js',
  'utils/clipboard.js', 'utils/editorNav.js', 'utils/config.js', 'utils/lastModel.js', 'components/home/memory.js',
  'hooks/useAudioPeaks.js',
]

for (const file of COPIES) {
  test(`${file} is a byte copy of the core file`, () => {
    assert.equal(read(join(compat, file)), read(join(core, file)))
  })
}

test('apiUrl and basePath keep the host rules, and the module loads without a document', async () => {
  const { apiUrl, basePath, routerBasename } = await import('../src/utils/basePath.js')
  assert.equal(basePath, '')
  assert.equal(routerBasename, '/')
  assert.equal(apiUrl('/v1/x'), '/v1/x')
  const host = read(join(core, 'utils/basePath.js'))
  // The host file's three rules, which the stand-in repeats.
  assert.match(host, /replace\(\/\\\/\+\$\/, ''\)/)
  assert.match(host, /startsWith\('http:\/\/'\) \|\| path\.startsWith\('https:\/\/'\)/)
  assert.match(host, /return basePath \+ path/)
})

test('the stand-ins sit at the paths the page code imports', () => {
  // A relative import that is not in the add-on's src/ is answered from here,
  // by the same relative path. These are the ones the pages use.
  for (const file of [
    'components/Icon.jsx', 'components/ModelSelector.jsx', 'components/PageHeader.jsx', 'components/ConfirmDialog.jsx',
    'components/LoadingSpinner.jsx', 'components/Modal.jsx', 'components/Lightbox.jsx', 'components/audio/WaveformPlayer.jsx',
    'components/biometrics/MediaInput.jsx', 'components/home/HomeModelPicker.jsx', 'context/AuthContext.jsx',
    'hooks/useModels.js', 'hooks/useResources.js', 'hooks/useObjectUrl.js', 'hooks/useVoiceProfiles.js',
    'hooks/useOperations.js', 'hooks/useMediaCapture.js', 'hooks/useMCPClient.js', 'utils/api.js',
  ]) assert.ok(read(join(compat, file)).length > 0, file)
})
