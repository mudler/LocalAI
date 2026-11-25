// SPDX-License-Identifier: MIT
// The module aliases of the app, shared by vite.config.mts and vitest.config.ts.
// tsconfig.json "paths" mirrors them by hand; keep them in step. One exception:
// tsconfig maps only "@localai/ui-kit/*", not the bare "@localai/ui-kit", so the
// type check uses the launcher's typed declaration (renderer/kit.d.ts) instead
// of the kit's untyped JavaScript.
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const repo = path.resolve(here, '..')
const nm = (p) => path.resolve(here, 'node_modules', p)

export const aliases = {
  '@launcher/renderer': path.resolve(repo, 'launcher/src/renderer'),
  '@launcher/shared': path.resolve(repo, 'launcher/src/shared'),
  '@launcher/core': path.resolve(repo, 'launcher/src/core'),
  '@localai/ui-kit': path.resolve(repo, 'core/http/react-ui/packages/ui-kit'),
  '@webui': path.resolve(repo, 'core/http/react-ui/src'),
  // Files reached through the aliases live outside this package, so their bare
  // imports would resolve from the launcher's or the WebUI's node_modules
  // (absent in CI). Pin each one to this package's copy; this also keeps a
  // single React in the bundle.
  react: nm('react'),
  'react-dom': nm('react-dom'),
  'react-router-dom': nm('react-router-dom'),
  'react-i18next': nm('react-i18next'),
  i18next: nm('i18next'),
  '@fontsource-variable/geist': nm('@fontsource-variable/geist'),
  '@fontsource-variable/geist-mono': nm('@fontsource-variable/geist-mono'),
}

export const repoRoot = repo
