import { test, expect } from './coverage-fixtures.js'
import { mockStudio, readStore, sampleHistory, seedHistory, stubMedia } from './studio-fixtures.js'

// "Your work" on the Studio front page: filters with counts, favourites,
// stacking related results, and clearing the history.

const tabs = (page) => page.locator('.studio-filter')
const count = (page, key) => page.locator(`.studio-filter[data-filter="${key}"] .studio-filter__n`)

async function open(page, opts = {}) {
  await mockStudio(page)
  await stubMedia(page)
  await seedHistory(page, sampleHistory(), opts)
  await page.goto('/app/studio')
  await expect(page.locator('[data-testid="studio-work"]')).toBeVisible()
}

test.describe('Your work', () => {
  test('counts every result per type, and the filters match', async ({ page }) => {
    await open(page)
    await expect(page.locator('[data-testid="studio-work-count"]')).toHaveText('9 results')
    const expected = { all: 9, favourites: 0, images: 4, video: 1, threed: 0, tts: 1, sound: 1, transform: 1, diarization: 1 }
    for (const [key, n] of Object.entries(expected)) await expect(count(page, key)).toHaveText(String(n))
    await page.locator('.studio-filter[data-filter="images"]').click()
    await expect(page.locator('[data-testid="work-tile"]')).toHaveCount(4)
    await expect(page.locator('[data-testid="work-tile"]:not([data-type="images"])')).toHaveCount(0)
    await page.locator('.studio-filter[data-filter="diarization"]').click()
    await expect(page.locator('[data-testid="work-tile"]')).toHaveCount(1)
  })

  test('a filter with nothing in it says so', async ({ page }) => {
    await open(page)
    await page.locator('.studio-filter[data-filter="threed"]').click()
    await expect(page.locator('[data-testid="studio-work-empty"]')).toContainText('No 3D yet')
  })

  test('only types this person can use are offered as filters', async ({ page }) => {
    await open(page)
    await expect(tabs(page)).toHaveCount(9)
    expect((await tabs(page).allTextContents()).map(t => t.replace(/\d+$/, ''))).toEqual(['All', 'Favourites', 'Images', 'Video', '3D', 'TTS', 'Sound', 'Transform', 'Diarization'])
  })

  test('related results stack into one project tile, and All counts the results inside it', async ({ page }) => {
    await open(page)
    const project = page.locator('[data-testid="work-project"]')
    await expect(project).toHaveCount(1)
    await expect(project).toHaveAttribute('data-count', '3')
    await expect(project).toContainText('3 results')
    // 9 results, one stack of three: seven tiles.
    await expect(page.locator('[data-testid="work-tile"], [data-testid="work-project"]')).toHaveCount(7)
  })

  test('the group switch flattens stacks into their results', async ({ page }) => {
    await open(page)
    await page.locator('[data-testid="studio-group-switch"]').click()
    await expect(page.locator('[data-testid="work-project"]')).toHaveCount(0)
    await expect(page.locator('[data-testid="work-tile"]')).toHaveCount(9)
    await page.locator('[data-testid="studio-group-switch"]').click()
    await expect(page.locator('[data-testid="work-project"]')).toHaveCount(1)
  })

  test('a type filter lists results, not stacks', async ({ page }) => {
    await open(page)
    await page.locator('.studio-filter[data-filter="video"]').click()
    await expect(page.locator('[data-testid="work-project"]')).toHaveCount(0)
    await expect(page.locator('[data-testid="work-tile"]')).toHaveCount(1)
  })

  test('a tile shows its title, model and age, with a picture for a picture', async ({ page }) => {
    await open(page)
    const forest = page.locator('[data-testid="work-tile"][data-id="img-forest"]')
    await expect(forest).toContainText('Misty pine forest')
    await expect(forest).toContainText('flux.1-schnell')
    await expect(forest).toContainText(/\d+h ago/)
    await expect(forest.locator('img')).toBeVisible()
    await expect(page.locator('[data-testid="work-tile"][data-id="tts-welcome"] .studio-wave')).toBeVisible()
    await expect(page.locator('[data-testid="work-tile"][data-id="dia-sync"] .studio-lanes i')).toHaveCount(3)
  })

  test('the star favourites a result, counts it, filters by it, and survives a reload', async ({ page }) => {
    await open(page)
    const star = page.locator('[data-testid="work-tile"][data-id="img-forest"] [data-testid="studio-star"]')
    await expect(star).toHaveAttribute('aria-pressed', 'false')
    await star.click()
    await expect(star).toHaveAttribute('aria-pressed', 'true')
    await expect(count(page, 'favourites')).toHaveText('1')
    // The click is a star, not an open.
    await expect(page.locator('[data-testid="studio-lineage"]')).toHaveCount(0)
    await page.locator('.studio-filter[data-filter="favourites"]').click()
    await expect(page.locator('[data-testid="work-tile"]')).toHaveCount(1)
    expect(await readStore(page, 'localai_studio_favourites')).toEqual(['img-forest'])
    await page.reload()
    await expect(count(page, 'favourites')).toHaveText('1')
    await page.locator('[data-testid="work-tile"][data-id="img-forest"] [data-testid="studio-star"]').click()
    await expect(count(page, 'favourites')).toHaveText('0')
    expect(await readStore(page, 'localai_studio_favourites')).toBeNull()
  })

  test('favourites seeded in storage show as set', async ({ page }) => {
    await open(page, { favourites: ['tts-welcome'] })
    await expect(count(page, 'favourites')).toHaveText('1')
  })

  test('Clear history asks first, and Cancel keeps everything', async ({ page }) => {
    await open(page)
    await page.locator('[data-testid="studio-clear-history"]').click()
    await expect(page.getByRole('alertdialog')).toContainText('Files already made stay on the server')
    await page.getByRole('button', { name: 'Cancel' }).click()
    await expect(page.locator('[data-testid="studio-work-count"]')).toHaveText('9 results')
  })

  test('Clear history removes the lists and favourites from this browser, and only those', async ({ page }) => {
    await open(page, { favourites: ['img-forest'] })
    await page.evaluate(() => localStorage.setItem('unrelated', 'keep'))
    await page.locator('[data-testid="studio-clear-history"]').click()
    await page.getByRole('alertdialog').getByRole('button', { name: 'Clear history' }).click()
    await expect(page.locator('[data-testid="studio-work-empty"]')).toContainText('Nothing here yet')
    await expect(page.locator('[data-testid="studio-work-count"]')).toHaveText('0 results')
    await expect(page.locator('[data-testid="studio-clear-history"]')).toHaveCount(0)
    for (const key of ['localai_image_history', 'localai_video_history', 'localai_tts_history', 'localai_sound_history', 'localai_audio_transform_history', 'localai_diarization_history', 'localai_studio_favourites']) {
      expect(await readStore(page, key)).toBeNull()
    }
    expect(await page.evaluate(() => localStorage.getItem('unrelated'))).toBe('keep')
    await page.reload()
    await expect(page.locator('[data-testid="studio-work-empty"]')).toBeVisible()
  })

  test('shows a skeleton while 3D history is read, not an empty state', async ({ page }) => {
    await mockStudio(page)
    await page.goto('/app/studio')
    await expect(page.locator('[data-testid="studio-work"]')).toBeVisible()
    // Once answered, the empty state is the truth.
    await expect(page.locator('[data-testid="studio-work-empty"]')).toBeVisible()
  })

  test('the masonry uses fewer columns on a narrow screen', async ({ page }) => {
    await open(page)
    const wide = await page.locator('.studio-masonry').getAttribute('data-columns')
    await page.setViewportSize({ width: 390, height: 844 })
    await expect.poll(() => page.locator('.studio-masonry').getAttribute('data-columns')).toBe('1')
    expect(Number(wide)).toBeGreaterThan(1)
  })
})
