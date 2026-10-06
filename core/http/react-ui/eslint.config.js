import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'

export default [
  { ignores: ['dist'] },
  {
    files: ['**/*.{js,jsx}'],
    languageOptions: {
      ecmaVersion: 2024,
      globals: globals.browser,
      parserOptions: {
        ecmaVersion: 'latest',
        ecmaFeatures: { jsx: true },
        sourceType: 'module',
      },
    },
    plugins: {
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
      // Without eslint-plugin-react's jsx-uses-vars the core rule cannot see a
      // component used only as JSX. Icon is on almost every page, so it is
      // exempt rather than adding one warning per import.
      'no-unused-vars': ['warn', { argsIgnorePattern: '^_', varsIgnorePattern: '^(Icon|FaIcon)$' }],
    },
  },
]
