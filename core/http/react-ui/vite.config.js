import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import istanbul from 'vite-plugin-istanbul'

const backendUrl = process.env.LOCALAI_URL || 'http://localhost:8080'

// COVERAGE=true produces an instrumented build whose modules report istanbul
// counters on window.__coverage__, harvested by the Playwright coverage
// fixture (e2e/coverage-fixtures.js). Off by default so normal/dev/prod builds
// carry no instrumentation overhead.
const coverage = process.env.COVERAGE === 'true'

export default defineConfig({
  plugins: [
    react(),
    ...(coverage
      ? [
          istanbul({
            include: 'src/**/*',
            extension: ['.js', '.jsx', '.ts', '.tsx'],
            requireEnv: false,
            // The e2e suite runs against `vite build` output, not the dev
            // server, so instrumentation must be applied to the production
            // build too (the plugin only instruments dev mode otherwise).
            forceBuildInstrument: true,
          }),
        ]
      : []),
  ],
  base: '/',
  server: {
    port: 3000,
    proxy: {
      '/api': backendUrl,
      '/v1': backendUrl,
      '/tts': backendUrl,
      '/video': backendUrl,
      '/backend': backendUrl,
      '/models': backendUrl,
      '/backends': backendUrl,
      '/swagger': backendUrl,
      '/static': backendUrl,
      '/generated-audio': backendUrl,
      '/generated-images': backendUrl,
      '/generated-videos': backendUrl,
      '/version': backendUrl,
      '/system': backendUrl,
    },
  },
  build: {
    outDir: 'dist',
    assetsDir: 'assets',
  },
})
