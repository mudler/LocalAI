import { test, expect } from '@playwright/test'

function mockCapabilities(page, capabilities) {
  return page.route('**/api/models/capabilities', (route) => {
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ data: capabilities }),
    })
  })
}

function mockImageGeneration(page, images) {
  return page.route('**/v1/images/generations', (route) => {
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        created: Math.floor(Date.now() / 1000),
        data: images,
      }),
    })
  })
}

function mockVideoGeneration(page, videos) {
  return page.route('**/video', (route) => {
    if (route.request().method() !== 'POST') return route.continue()
    route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        created: Math.floor(Date.now() / 1000),
        data: videos,
      }),
    })
  })
}

function mockTTSGeneration(page, filename) {
  return page.route('**/tts', (route) => {
    if (route.request().method() !== 'POST') return route.continue()
    const wavHeader = new Uint8Array(44) // minimal WAV header
    route.fulfill({
      status: 200,
      headers: {
        'Content-Type': 'audio/wav',
        'Content-Disposition': `attachment; filename="${filename}"`,
      },
      body: Buffer.from(wavHeader),
    })
  })
}

function mockSoundGeneration(page, filename) {
  return page.route('**/v1/sound-generation', (route) => {
    if (route.request().method() !== 'POST') return route.continue()
    const wavHeader = new Uint8Array(44)
    route.fulfill({
      status: 200,
      headers: {
        'Content-Type': 'audio/wav',
        'Content-Disposition': `attachment; filename="${filename}"`,
      },
      body: Buffer.from(wavHeader),
    })
  })
}

test.describe('Media History - Image Generation', () => {
  test.beforeEach(async ({ page }) => {
    await mockCapabilities(page, [{ id: 'test-image-model', capabilities: ['FLAG_IMAGE'] }])
  })

  test('history entry appears after image generation', async ({ page }) => {
    await mockImageGeneration(page, [{ url: '/generated-images/test.png' }])

    await page.goto('/app/image')
    await expect(page.getByRole('button', { name: 'test-image-model' })).toBeVisible({ timeout: 10_000 })

    await page.locator('.textarea').first().fill('a beautiful sunset')
    await page.locator('button[type="submit"]').click()

    // Image should appear in preview
    await expect(page.locator('.media-result-grid img')).toBeVisible({ timeout: 10_000 })

    // History entry should appear
    await expect(page.getByTestId('media-history-item')).toHaveCount(1)
    await expect(page.getByTestId('media-history-item')).toContainText('a beautiful sunset')
  })

  test('clicking history entry loads image in preview', async ({ page }) => {
    let callCount = 0
    await page.route('**/v1/images/generations', (route) => {
      callCount++
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          created: Math.floor(Date.now() / 1000),
          data: [{ url: `/generated-images/img${callCount}.png` }],
        }),
      })
    })

    await page.goto('/app/image')
    await expect(page.getByRole('button', { name: 'test-image-model' })).toBeVisible({ timeout: 10_000 })

    // Generate first image
    await page.locator('.textarea').first().fill('first prompt')
    await page.locator('button[type="submit"]').click()
    await expect(page.locator('.media-result-grid img')).toBeVisible({ timeout: 10_000 })

    // Generate second image
    await page.locator('.textarea').first().fill('second prompt')
    await page.locator('button[type="submit"]').click()
    await expect(page.locator('.media-result-grid img[src="/generated-images/img2.png"]')).toBeVisible({ timeout: 10_000 })

    // Click first history entry (second in list since newest first)
    const items = page.getByTestId('media-history-item')
    await expect(items).toHaveCount(2)
    await items.last().click()

    // Preview should show the first image
    await expect(page.locator('.media-result-grid img[src="/generated-images/img1.png"]')).toBeVisible()
  })

  test('history persists across navigation', async ({ page }) => {
    await mockImageGeneration(page, [{ url: '/generated-images/persist.png' }])

    await page.goto('/app/image')
    await expect(page.getByRole('button', { name: 'test-image-model' })).toBeVisible({ timeout: 10_000 })

    await page.locator('.textarea').first().fill('persist test')
    await page.locator('button[type="submit"]').click()
    await expect(page.getByTestId('media-history-item')).toHaveCount(1, { timeout: 10_000 })

    // Wait for debounced save
    await page.waitForTimeout(600)

    // Navigate away and back
    await page.goto('/app')
    await page.goto('/app/image')

    // Re-register the mock for capabilities after navigation
    await expect(page.getByTestId('media-history')).toBeVisible({ timeout: 10_000 })
    await expect(page.getByTestId('media-history-item')).toHaveCount(1)
    await expect(page.getByTestId('media-history-item')).toContainText('persist test')
  })

  test('delete removes a history entry', async ({ page }) => {
    await mockImageGeneration(page, [{ url: '/generated-images/del.png' }])

    await page.goto('/app/image')
    await expect(page.getByRole('button', { name: 'test-image-model' })).toBeVisible({ timeout: 10_000 })

    await page.locator('.textarea').first().fill('delete me')
    await page.locator('button[type="submit"]').click()
    await expect(page.getByTestId('media-history-item')).toHaveCount(1, { timeout: 10_000 })

    // Hover and click delete
    await page.getByTestId('media-history-item').hover()
    await page.getByTestId('media-history-delete').click()

    await expect(page.getByTestId('media-history-item')).toHaveCount(0)
  })

  test('clear all removes all entries', async ({ page }) => {
    let callCount = 0
    await page.route('**/v1/images/generations', (route) => {
      callCount++
      route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          created: Math.floor(Date.now() / 1000),
          data: [{ url: `/generated-images/img${callCount}.png` }],
        }),
      })
    })

    await page.goto('/app/image')
    await expect(page.getByRole('button', { name: 'test-image-model' })).toBeVisible({ timeout: 10_000 })

    // Generate two images
    await page.locator('.textarea').first().fill('first')
    await page.locator('button[type="submit"]').click()
    await expect(page.getByTestId('media-history-item')).toHaveCount(1, { timeout: 10_000 })

    await page.locator('.textarea').first().fill('second')
    await page.locator('button[type="submit"]').click()
    await expect(page.getByTestId('media-history-item')).toHaveCount(2, { timeout: 10_000 })

    // Click clear all
    await page.locator('.media-history-clear-btn').click()
    await expect(page.getByTestId('media-history-item')).toHaveCount(0)
  })
})

test.describe('Media History - TTS', () => {
  test.beforeEach(async ({ page }) => {
    await mockCapabilities(page, [{ id: 'test-tts-model', capabilities: ['FLAG_TTS'] }])
  })

  test('TTS history entry appears with server URL from Content-Disposition', async ({ page }) => {
    await mockTTSGeneration(page, 'tts.wav')

    await page.goto('/app/tts')
    await expect(page.getByRole('button', { name: 'test-tts-model' })).toBeVisible({ timeout: 10_000 })

    await page.locator('.textarea').fill('hello world')
    await page.locator('button[type="submit"]').click()

    // History entry should appear
    await expect(page.getByTestId('media-history-item')).toHaveCount(1, { timeout: 10_000 })
    await expect(page.getByTestId('media-history-item')).toContainText('hello world')

    // Click the history entry to load it
    await page.getByTestId('media-history-item').click()

    // Audio element should use server URL
    await expect(page.getByTestId('history-audio')).toHaveAttribute('src', '/generated-audio/tts.wav')
  })
})

test.describe('Media History - Sound Generation', () => {
  test.beforeEach(async ({ page }) => {
    await mockCapabilities(page, [{ id: 'test-sound-model', capabilities: ['FLAG_SOUND_GENERATION'] }])
  })

  test('Sound generation history entry appears', async ({ page }) => {
    await mockSoundGeneration(page, 'sound.wav')

    await page.goto('/app/sound')
    await expect(page.getByRole('button', { name: 'test-sound-model' })).toBeVisible({ timeout: 10_000 })

    await page.locator('.textarea').first().fill('upbeat jazz')
    await page.locator('button[type="submit"]').click()

    await expect(page.getByTestId('media-history-item')).toHaveCount(1, { timeout: 10_000 })
    await expect(page.getByTestId('media-history-item')).toContainText('upbeat jazz')
  })
})

test.describe('Media History - Video Generation', () => {
  test.beforeEach(async ({ page }) => {
    await mockCapabilities(page, [{ id: 'test-video-model', capabilities: ['FLAG_VIDEO'] }])
  })

  test('Video history entry appears after generation', async ({ page }) => {
    await mockVideoGeneration(page, [{ url: '/generated-videos/test.mp4' }])

    await page.goto('/app/video')
    await expect(page.getByRole('button', { name: 'test-video-model' })).toBeVisible({ timeout: 10_000 })

    await page.locator('.textarea').first().fill('a running cat')
    await page.locator('button[type="submit"]').click()

    await expect(page.getByTestId('media-history-item')).toHaveCount(1, { timeout: 10_000 })
    await expect(page.getByTestId('media-history-item')).toContainText('a running cat')
  })
})
