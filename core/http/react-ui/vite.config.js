import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const backendUrl = process.env.LOCALAI_URL || 'http://localhost:8080'

export default defineConfig({
  plugins: [react()],
  base: '/app',
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
