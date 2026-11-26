// SPDX-License-Identifier: MIT
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import { aliases } from './aliases.mjs'

export default defineConfig({
  plugins: [react()],
  // The UI kit's .jsx files sit outside this package, where the React plugin's
  // default does not reach: compile them with the automatic runtime too.
  esbuild: { jsx: 'automatic' },
  resolve: { alias: aliases },
  test: {
    include: ['test/**/*.test.ts', 'test/**/*.test.tsx'],
    environment: 'node',
    testTimeout: 20000,
    css: false,
  },
})
