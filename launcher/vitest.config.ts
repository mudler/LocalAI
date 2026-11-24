import path from 'node:path'
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// Main-process tests run under plain Node. Renderer component tests are
// .test.tsx files that opt into jsdom with a `@vitest-environment jsdom`
// comment; the React plugin compiles the UI kit's .jsx files for them.
const nm = (p: string) => path.resolve(__dirname, 'node_modules', p)

export default defineConfig({
  plugins: [react()],
  // The kit's .jsx files sit outside this package, where the React plugin's
  // default does not reach: compile them with the automatic runtime too.
  esbuild: { jsx: 'automatic' },
  resolve: {
    alias: {
      '@localai/ui-kit': path.resolve(__dirname, '../core/http/react-ui/packages/ui-kit'),
      react: nm('react'),
      'react-dom': nm('react-dom'),
    },
  },
  test: {
    include: ['test/**/*.test.ts', 'test/**/*.test.tsx'],
    environment: 'node',
    testTimeout: 20000,
    css: false,
  },
})
