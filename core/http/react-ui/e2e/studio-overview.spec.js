import { test, expect } from './coverage-fixtures.js'

// Studio overview (src/pages/StudioOverview.jsx).
//
// Studio was a tab strip over six generators that opened on Images and told you
// nothing about what this machine could actually run. The tests are about that:
// what the strip reports before you click, and the difference between a
// modality that is switched off and one that merely has no model. The front
// page is now a composer with a chip per type, so "a lane" reads "a chip":
// a type with a model is solid, a type without one is dashed.

const OVERVIEW = '[data-testid="studio-overview"]'
const CHIP = (key) => `.studio-types .studio-type[data-type="${key}"]`
const tabFor = (page, key) => page.locator(`.studio-tab[data-tab="${key}"]`)

const model = (id, ...capabilities) => ({ id, capabilities })

// Images and speech covered, video and sound not. 3D and transform are feature
// flags rather than models, so they are controlled separately.
const SOME_MODELS = {
  data: [
    model('flux.1-schnell', 'FLAG_IMAGE'),
    model('kokoro-82m', 'FLAG_TTS'),
    model('qwen3-8b', 'FLAG_CHAT'),
  ],
}

async function mockCapabilities(page, payload = SOME_MODELS) {
  await page.route('**/api/models/capabilities', route => route.fulfill({ json: payload }))
}

test.describe('Studio overview', () => {
  test('Studio opens on the overview rather than dropping into Images', async ({ page }) => {
    await mockCapabilities(page)
    await page.goto('/app/studio')
    await expect(page.locator(OVERVIEW)).toBeVisible()
  })

  test('a generator path opens that generator', async ({ page }) => {
    await mockCapabilities(page)
    await page.goto('/app/studio/images')
    await expect(page.locator(OVERVIEW)).toHaveCount(0)
    await expect(page.locator('[data-testid="studio-workspace"]')).toBeVisible()
  })

  test('an unrecognised tab falls back to the overview, not to Images', async ({ page }) => {
    await mockCapabilities(page)
    await page.goto('/app/studio/nonsense')
    await expect(page.locator(OVERVIEW)).toBeVisible()
  })

  test('the tab strip reports which modalities have a model', async ({ page }) => {
    await mockCapabilities(page)
    await page.goto('/app/studio')
    // Filled: something installed advertises the capability.
    await expect(tabFor(page, 'images').locator('.studio-tab__dot--on')).toBeVisible()
    await expect(tabFor(page, 'tts').locator('.studio-tab__dot--on')).toBeVisible()
    // Hollow: the modality is available, nothing serves it yet.
    await expect(tabFor(page, 'video').locator('.studio-tab__dot--off')).toBeVisible()
    await expect(tabFor(page, 'sound').locator('.studio-tab__dot--off')).toBeVisible()
  })

  test('the tab dots are quiet and the state is spoken, not only drawn', async ({ page }) => {
    await mockCapabilities(page)
    await page.goto('/app/studio')
    await expect(tabFor(page, 'images')).toContainText('(model installed)')
    await expect(tabFor(page, 'video')).toContainText('(no model installed)')
    await expect(page.getByRole('button', { name: /^Images \(model installed\)$/ })).toBeVisible()
    // Neither state is drawn in the success colour.
    const colours = await page.evaluate(() => {
      const root = getComputedStyle(document.documentElement)
      const success = root.getPropertyValue('--color-success').trim()
      const probe = document.createElement('i')
      probe.style.color = success
      document.body.appendChild(probe)
      const resolved = getComputedStyle(probe).color
      probe.remove()
      return {
        success: resolved,
        on: getComputedStyle(document.querySelector('.studio-tab__dot--on')).backgroundColor,
      }
    })
    expect(colours.on).not.toBe(colours.success)
  })

  test('a modality with no model offers a way to install one', async ({ page }) => {
    await mockCapabilities(page)
    await page.goto('/app/studio')
    const video = page.locator(CHIP('video'))
    await expect(video).toBeVisible()
    // Dashed, because nothing serves it. Picking it is how you learn what it needs.
    await expect(video).toHaveAttribute('data-missing', 'true')
    await expect(page.locator('[data-testid="studio-install-note"]')).toHaveCount(0)
    await video.click()
    await expect(page.locator('[data-testid="studio-install-note"]')).toBeVisible()
  })

  test('a modality with a model names it instead of offering an install', async ({ page }) => {
    await mockCapabilities(page)
    await page.goto('/app/studio')
    const images = page.locator(CHIP('images'))
    await expect(images).not.toHaveAttribute('data-missing', 'true')
    await expect(page.locator('[data-testid="studio-model"]')).toHaveValue('flux.1-schnell')
    await expect(page.locator('[data-testid="studio-install-note"]')).toHaveCount(0)
  })

  test('a disabled feature gets no tab and no chip at all', async ({ page }) => {
    // Switched off is a different thing from "no model installed", and
    // conflating them is how someone ends up staring at a control that cannot
    // work. 3d is a permission rather than an /api/features entry, and
    // hasFeature() short-circuits to true for admins and for auth-off
    // installations, so withholding it needs a real non-admin session.
    await page.route('**/api/auth/status', route => route.fulfill({
      json: {
        authEnabled: true,
        user: { name: 'someone', role: 'user', permissions: { images: true, video: true, tts: true, sound: true } },
      },
    }))
    await mockCapabilities(page)
    await page.goto('/app/studio')
    await expect(page.locator(OVERVIEW)).toBeVisible()
    await expect(tabFor(page, 'threed')).toHaveCount(0)
    await expect(page.locator(CHIP('threed'))).toHaveCount(0)
  })

  test('asks the capabilities endpoint once, not once per modality', async ({ page }) => {
    let calls = 0
    await page.route('**/api/models/capabilities', route => {
      calls += 1
      route.fulfill({ json: SOME_MODELS })
    })
    await page.goto('/app/studio')
    await expect(page.locator(OVERVIEW)).toBeVisible()
    await page.waitForTimeout(500)
    // useModels() fetches the whole list and filters in the browser, so one
    // hook per modality would be six identical requests on every mount.
    expect(calls).toBe(1)
  })

  test('an installation with no models at all still renders every modality', async ({ page }) => {
    await mockCapabilities(page, { data: [] })
    await page.goto('/app/studio')
    await expect(page.locator(OVERVIEW)).toBeVisible()
    await expect(page.locator('.studio-types .studio-type').first()).toBeVisible()
    await expect(page.locator('.studio-types .studio-type[data-missing="true"]')).toHaveCount(7)
    await expect(page.locator('.studio-tab__dot--on')).toHaveCount(0)
  })

  test('earlier results surface in Your work', async ({ page }) => {
    await mockCapabilities(page)
    // History is localStorage, written by each generator. The front page is the
    // first place it is read across modalities rather than within one.
    await page.addInitScript(() => {
      localStorage.setItem('localai_image_history', JSON.stringify([
        { id: 'i1', createdAt: Date.now(), model: 'flux.1-schnell', prompt: 'a brass orrery', results: [] },
      ]))
    })
    await page.goto('/app/studio')
    const tile = page.locator('[data-testid="work-tile"]')
    await expect(tile).toHaveCount(1)
    await expect(tile).toContainText('flux.1-schnell')
    await expect(tile).toContainText('a brass orrery')
  })

  test('no history means no tiles, and an empty state that says what happens next', async ({ page }) => {
    await mockCapabilities(page)
    await page.goto('/app/studio')
    await expect(page.locator('[data-testid="work-tile"]')).toHaveCount(0)
    await expect(page.locator('[data-testid="studio-work-empty"]')).toContainText('Nothing here yet')
  })

  test('the overview is reachable back from a generator tab', async ({ page }) => {
    await mockCapabilities(page)
    await page.goto('/app/studio/images')
    await tabFor(page, 'overview').click()
    await expect(page.locator(OVERVIEW)).toBeVisible()
  })

  test('a legacy ?tab= link is redirected to its path', async ({ page }) => {
    // Bookmarks and older docs still use the query form; they must keep working
    // and must land on the canonical URL rather than a second spelling of it.
    await mockCapabilities(page)
    await page.goto('/app/studio?tab=images')
    await expect(page).toHaveURL(/\/app\/studio\/images$/)
    await expect(page.locator('[data-testid="studio-workspace"]')).toBeVisible()
  })
})
