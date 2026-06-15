import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  retries: process.env.CI ? 2 : 0,
  // TEMPORARY: cap parallelism. Playwright's default (cores/2) oversubscribes
  // high-core dev machines and intermittently starves the page-teardown
  // coverage harvest past the 30s test timeout (flaky "Tearing down page"
  // failures, different specs each run). Capped at 8 pending a proper
  // root-cause fix; override with PW_WORKERS.
  workers: process.env.PW_WORKERS ? Number(process.env.PW_WORKERS) : 8,
  reporter: process.env.CI ? 'html' : 'list',
  use: {
    baseURL: 'http://127.0.0.1:8089',
    trace: 'on-first-retry',
  },
  projects: [
    {
      name: 'chromium',
      use: {
        browserName: 'chromium',
        // Use a nix-provided Chromium when PLAYWRIGHT_CHROMIUM_PATH is set
        // (the flake dev shell exports it). Avoids Playwright's downloaded
        // browser, which can't resolve system libs (libglib-2.0, …) on NixOS.
        // Unset in CI, where `playwright install --with-deps` is used instead.
        ...(process.env.PLAYWRIGHT_CHROMIUM_PATH
          ? { launchOptions: { executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH } }
          : {}),
      },
    },
  ],
  webServer: process.env.PLAYWRIGHT_EXTERNAL_SERVER ? undefined : {
    command: '../../../tests/e2e-ui/ui-test-server --mock-backend=../../../tests/e2e/mock-backend/mock-backend --port=8089 > /tmp/ui-test-server.log 2>&1',
    port: 8089,
    timeout: 120_000,
    reuseExistingServer: !process.env.CI,
  },
})
