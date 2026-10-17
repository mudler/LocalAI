import { defineConfig } from '@playwright/test'

// Configuration of the add-on tools suite (scripts/addons/tables_tools.sh).
// The runner starts the UI test server with a real add-on owner, the real
// Tables executable and the scripted mock model on an isolated port, and
// passes its address in ADDONS_UI_URL and the seeded accounts in
// ADDONS_FIXTURE. The specs change the owner's state, so they run in order on
// one worker.
export default defineConfig({
  testDir: './e2e',
  testMatch: ['addons-proposals.spec.js'],
  timeout: 120_000,
  retries: 0,
  workers: 1,
  fullyParallel: false,
  reporter: [['json', { outputFile: process.env.ADDONS_UI_REPORT || 'addons-proposals-report.json' }]],
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
