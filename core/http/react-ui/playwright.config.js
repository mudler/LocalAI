import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? 'html' : 'list',
  use: {
    baseURL: 'http://127.0.0.1:8089',
    trace: 'on-first-retry',
  },
  projects: [
    {
      name: 'chromium',
      use: { browserName: 'chromium' },
    },
  ],
  webServer: process.env.PLAYWRIGHT_EXTERNAL_SERVER ? undefined : {
    command: '../../../tests/e2e-ui/ui-test-server --mock-backend=../../../tests/e2e/mock-backend/mock-backend --port=8089 > /tmp/ui-test-server.log 2>&1',
    port: 8089,
    timeout: 120_000,
    reuseExistingServer: !process.env.CI,
  },
})
