// SPDX-License-Identifier: MIT
import path from 'node:path'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { aliases, repoRoot } from './aliases.mjs'

export default defineConfig({
  root: __dirname,
  base: './',
  plugins: [react()],
  resolve: { alias: aliases },
  server: { fs: { allow: [repoRoot] } },
  build: {
    outDir: path.resolve(__dirname, 'dist'),
    emptyOutDir: true,
    sourcemap: false,
  },
})
