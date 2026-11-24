import { defineConfig } from '@playwright/test'

// Configuration of the Tables CSV export suite (scripts/addons/tables_csv.sh). The
// runner starts two UI test servers on isolated ports, both with a real
// add-on owner, the real Tables executable and the built UI package: one
// with named accounts (ADDONS_UI_URL, seeded accounts in ADDONS_FIXTURE) and
// one in local-owner mode (ADDONS_LOCAL_URL). The specs change the owner's
// state, so they run in order on one worker.
export default defineConfig({
  testDir: './e2e',
  testMatch: ['tables-artifacts.spec.js'],
  timeout: 180_000,
  retries: 0,
  workers: 1,
  fullyParallel: false,
  reporter: [['json', { outputFile: process.env.ADDONS_UI_REPORT || 'tables-artifacts-report.json' }]],
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
