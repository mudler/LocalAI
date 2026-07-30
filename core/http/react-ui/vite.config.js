import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import istanbul from 'vite-plugin-istanbul'

const backendUrl = process.env.LOCALAI_URL || 'http://localhost:8080'

// COVERAGE=true produces an instrumented build whose modules report istanbul
// counters on window.__coverage__, harvested by the Playwright coverage
// fixture (e2e/coverage-fixtures.js). Off by default so normal/dev/prod builds
// carry no instrumentation overhead.
const coverage = process.env.COVERAGE === 'true'
// COVERAGE_V8=true produces a NON-instrumented build with source maps, so the
// Playwright coverage fixture can collect Chromium V8 coverage (near-zero
// runtime overhead, unlike istanbul's build-time counters) and map it back to
// source via v8-to-istanbul. Mutually exclusive with COVERAGE.
const coverageV8 = process.env.COVERAGE_V8 === 'true'

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
  // Relative base so every generated URL (entry scripts in index.html, CSS
  // `url()` font references, and lazily-imported route chunks) resolves against
  // the file that references it rather than the origin root. When LocalAI is
  // served under a reverse-proxy subpath (X-Forwarded-Prefix, e.g. `/llm/`),
  // an absolute `/assets/...` bypasses the prefix and 404s — breaking fonts
  // ("tofu" glyphs) and lazy-loaded chunks. index.html's now-relative refs
  // resolve via the `<base href>` that serveIndex always injects (see
  // core/http/app.go), so both proxied and root deployments load correctly.
  base: './',
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
      '/generated-3d': backendUrl,
      '/3d': backendUrl,
      '/version': backendUrl,
      '/system': backendUrl,
    },
  },
  build: {
    outDir: 'dist',
    assetsDir: 'assets',
    // Source maps are needed only to map V8 coverage back to original sources.
    sourcemap: coverageV8,
    rollupOptions: {
      output: {
        // The coverage build inlines all dynamic imports into a single chunk.
        // The app is route-code-split (router.jsx uses React.lazy), so a normal
        // build emits ~50 lazy chunks. V8 coverage only sees chunks a test
        // actually loaded, so untested pages would silently drop out of the
        // denominator and inflate the percentage. Bundling everything into one
        // chunk for the coverage build keeps the denominator complete and the
        // measurement invariant to how production is split. Production builds
        // (COVERAGE_V8 unset) keep code-splitting for fast first paint.
        inlineDynamicImports: coverageV8,
      },
    },
  },
})
