import { defineConfig } from '@playwright/test'

// Configuration of the add-ons gallery suite (scripts/addons/gallery_ui.sh).
// The runner starts the UI test server on an isolated port and passes its
// address in ADDONS_UI_URL, so this config never starts or reuses a server.
export default defineConfig({
  testDir: './e2e',
  testMatch: ['addons-gallery.spec.js'],
  timeout: 30_000,
  retries: 0,
  workers: 2,
  reporter: [['json', { outputFile: process.env.ADDONS_UI_REPORT || 'addons-ui-report.json' }]],
  use: {
    baseURL: process.env.ADDONS_UI_URL,
  },
  projects: [
    {
      name: 'chromium',
      use: {
        browserName: 'chromium',
        ...(process.env.PLAYWRIGHT_CHROMIUM_PATH
          ? { launchOptions: { executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH } }
          : {}),
      },
    },
  ],
})
