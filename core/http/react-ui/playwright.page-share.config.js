import { defineConfig } from '@playwright/test'
import base from './playwright.config.js'

// The page share spec starts its own local-ai binary with authentication on
// (LOCALAI_BIN and MOCK_BACKEND_BIN), so no web server and no global setup.
export default defineConfig({
  ...base,
  globalSetup: undefined,
  testIgnore: [],
  testMatch: ['**/page-share-real.spec.js'],
  workers: 1,
  timeout: 120_000,
  webServer: undefined,
})
