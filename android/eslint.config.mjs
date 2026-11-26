// SPDX-License-Identifier: MIT
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import js from '@eslint/js'
import globals from 'globals'
import tseslint from 'typescript-eslint'

const appRoot = path.dirname(fileURLToPath(import.meta.url))

// The app reaches launcher code only through the @launcher/* aliases, so the
// shared surface stays visible in one place. A relative import that leaves
// android/ would bypass that, so it is an error.
const staysInApp = {
  meta: { type: 'problem', messages: { leaves: 'Import "{{source}}" leaves android/; use @launcher/renderer, @launcher/shared or @launcher/core.' } },
  create(context) {
    const check = (node) => {
      const source = node.source && node.source.value
      if (typeof source !== 'string' || !source.startsWith('.')) return
      const target = path.resolve(path.dirname(context.filename), source)
      const rel = path.relative(appRoot, target)
      if (rel.startsWith('..') || path.isAbsolute(rel)) context.report({ node: node.source, messageId: 'leaves', data: { source } })
    }
    return { ImportDeclaration: check, ExportAllDeclaration: check, ExportNamedDeclaration: check, ImportExpression: check }
  },
}

export default tseslint.config(
  { ignores: ['node_modules/**', 'dist/**', 'native/**', 'p2pmobile/**'] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    languageOptions: { globals: { ...globals.browser, ...globals.node } },
    plugins: { local: { rules: { 'stays-in-app': staysInApp } } },
    rules: {
      'local/stays-in-app': 'error',
      'no-restricted-imports': [
        'error',
        {
          patterns: [
            { group: ['**/launcher/src/main', '**/launcher/src/main/**'], message: 'The desktop main process is not shared with the app.' },
            { group: ['**/launcher/src/preload', '**/launcher/src/preload/**'], message: 'The desktop preload is not shared with the app.' },
            { group: ['@launcher/*', '!@launcher/renderer', '!@launcher/shared', '!@launcher/core'], message: 'Only @launcher/renderer, @launcher/shared and @launcher/core exist.' },
            // A ".." segment would walk out of an allowed root (for example to src/main).
            { regex: '^@launcher/(.*/)?\\.\\.(/|$)', message: 'No ".." through a @launcher/* alias.' },
          ],
        },
      ],
    },
  },
)
