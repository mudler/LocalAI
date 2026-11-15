import path from 'node:path'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The WebUI sources the launcher borrows (theme CSS and UI-kit components).
// Aliasing into them is the spike's way to test sharing; see README.md.
const webui = path.resolve(__dirname, '../core/http/react-ui/src')
// The shared UI kit package (controls, CSS, icons, palette), imported by path.
const uikit = path.resolve(__dirname, '../core/http/react-ui/packages/ui-kit')
const nm = (p: string) => path.resolve(__dirname, 'node_modules', p)

export default defineConfig({
  root: path.resolve(__dirname, 'src/renderer'),
  base: './',
  plugins: [react()],
  resolve: {
    alias: {
      '@localai/ui-kit': uikit,
      '@webui': webui,
      // The WebUI folder has no node_modules of its own. Files imported through
      // the alias resolve bare imports from there, so each one is pinned to the
      // launcher's copy (this also keeps a single React in the bundle).
      react: nm('react'),
      'react-dom': nm('react-dom'),
      'react-router-dom': nm('react-router-dom'),
      'react-i18next': nm('react-i18next'),
      i18next: nm('i18next'),
    },
  },
  server: { fs: { allow: [path.resolve(__dirname, '..')] } },
  build: {
    outDir: path.resolve(__dirname, 'dist/renderer'),
    emptyOutDir: true,
    sourcemap: false,
  },
})
