import { defineConfig } from '@playwright/test'

// Configuration of the host slots suite (scripts/addons/host_slots.sh). The
// runner starts the UI test server with a real add-on owner on an isolated
// port and passes its address in ADDONS_UI_URL and the seeded accounts in
// ADDONS_FIXTURE. The specs change the owner's state (disable, enable), so
// they run in order on one worker.
export default defineConfig({
  testDir: './e2e',
  testMatch: ['addons-slots.spec.js'],
  timeout: 90_000,
  retries: 0,
  workers: 1,
  fullyParallel: false,
  reporter: [['json', { outputFile: process.env.ADDONS_UI_REPORT || 'addons-slots-report.json' }]],
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
