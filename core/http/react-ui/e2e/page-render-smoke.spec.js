import { test, expect } from './coverage-fixtures.js'
import { installStudioAddons } from './studio-addon.js'

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
  ['/app/usage', 'Usage'],
  ['/app/traffic', 'Traffic overview'],
  ['/app/traffic/models', 'Traffic models'],
  ['/app/traffic/host', 'GPU and host'],
  ['/app/traffic/prometheus', 'Prometheus'],
  ['/app/traces', 'Traces'],
  ['/app/middleware', 'Middleware'],
  ['/app/account', 'Account'],
  ['/app/models', 'Models'],
  ['/app/operate', 'Operate overview'],
  ['/app/backends', 'Backends'],
  ['/app/activity', 'Activity'],
  ['/app/settings', 'Settings'],
  ['/app/nodes', 'Nodes'],
  ['/app/scheduling', 'Placement rules'],
  ['/app/failover', 'Failover'],
  ['/app/nodes/add', 'Add a node'],
  ['/app/p2p', 'P2P'],
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
      await expect(page.locator('.page-title, .view-bar__title, .empty-state-title, .dk-empty-title, .op-status__title, .op-runtime h1, .sw-page h1, .tf-title, .st-title').first()).toBeVisible({ timeout: 15_000 })
      await expect(page).toHaveURL(new RegExp(path.replace(/\//g, '\\/') + '$'))
    })
  }
})

// Studio and Talk are add-ons: the pages draw inside the add-on shell once the
// built bundles are installed, and the old addresses open them there.
const ADDON_PAGES = [
  ['/app/addons/talk/talk', 'Talk', '.talk-page'],
  ['/app/addons/studio/studio', 'Studio', '[data-testid="studio-overview"]'],
  ['/app/addons/studio/studio/threed', 'Studio 3D', '.ws-title'],
  ['/app/talk', 'Talk (old address)', '.talk-page'],
  ['/app/studio/threed', 'Studio 3D (old address)', '.ws-title'],
]

test.describe('Add-on page render smoke', () => {
  for (const [path, label, marker] of ADDON_PAGES) {
    test(`renders ${label} (${path})`, async ({ page }) => {
      await installStudioAddons(page)
      await page.goto(path)
      await expect(page.locator(marker).first()).toBeVisible({ timeout: 15_000 })
      await expect(page).toHaveURL(/\/app\/addons\/(studio|talk)\//)
    })
  }
})
