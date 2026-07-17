import { test, expect } from './coverage-fixtures.js'

// Render-smoke coverage. Each page is lazy-loaded and runs its full render +
// initial effects on mount, so a bare visit captures the bulk of a page's
// lines — cheap, real coverage for pages that have no dedicated spec yet.
//
// This is the project's preferred way to keep the UI coverage gate green:
// raise the floor by covering more, rather than loosening the gate's
// tolerance (see CONTRIBUTING.md → "React UI coverage"). Auth is disabled in
// the test server, so RequireAdmin/RequireFeature resolve to isAdmin=true and
// every gated route renders without an auth/capability mock.
//
// Asserts the page mounted (its .page-title header is visible) and that it did
// not bounce to a gate redirect (/login or back to /app home).
const PAGES = [
  ['/app/talk', 'Talk'],
  ['/app/usage', 'Usage'],
  ['/app/account', 'Account'],
  ['/app/studio', 'Studio'],
  ['/app/manage', 'Manage'],
  ['/app/backends', 'Backends'],
  ['/app/settings', 'Settings'],
  ['/app/nodes', 'Nodes'],
  ['/app/scheduling', 'Scheduling'],
  ['/app/face', 'Face recognition'],
  ['/app/voice', 'Voice recognition'],
  ['/app/fine-tune', 'Fine-tuning'],
  ['/app/quantize', 'Quantize'],
  ['/app/voice-library', 'Voice Library'],
  ['/app/voice-library/new', 'Create voice profile'],
]

test.describe('Page render smoke', () => {
  for (const [path, label] of PAGES) {
    test(`renders ${label} (${path})`, async ({ page }) => {
      await page.goto(path)
      // .page-title for the normal header; .empty-state-title for pages that
      // render a gated/empty state (e.g. Account when auth is disabled).
      await expect(page.locator('.page-title, .empty-state-title').first()).toBeVisible({ timeout: 15_000 })
      await expect(page).toHaveURL(new RegExp(path.replace(/\//g, '\\/') + '$'))
    })
  }
})
