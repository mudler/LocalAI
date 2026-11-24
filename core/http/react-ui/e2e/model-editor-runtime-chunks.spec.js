import { test, expect } from './coverage-fixtures.js'

// The Runtime tuning section brings its own stylesheet in the model editor's
// chunk. On the production bundle (CSS per chunk) the section must load with
// its rules applied and no error, against the real test server: only the model
// file is stubbed, so the editor opens a llama-cpp model; the /api/llama-cpp
// routes are the real handlers.
//
// Tagged @production-chunks so `make test-ui-stale-chunk` (production build)
// runs it, and the coverage run (inlined build) skips it.

const CONFIG = 'name: chunk-check\nbackend: llama-cpp\nparameters:\n  model: chunk-check.gguf\ncontext_size: 4096\n'

test.describe('@production-chunks Runtime tuning on the shipped bundle', () => {
  test('the section loads with its stylesheet and no error', async ({ page }) => {
    const problems = []
    page.on('pageerror', err => problems.push(`page error: ${err.message}`))
    page.on('console', msg => {
      if (msg.type() === 'error' && !msg.text().startsWith('Failed to load resource')) problems.push(`console error: ${msg.text()}`)
    })
    page.on('response', res => {
      const { pathname } = new URL(res.url())
      if (pathname.startsWith('/assets/') && res.status() >= 400) problems.push(`asset ${pathname}: ${res.status()}`)
      if (pathname.startsWith('/api/llama-cpp/') && res.status() >= 400) problems.push(`${pathname}: ${res.status()}`)
    })
    await page.route('**/api/models/edit/chunk-check', route => route.fulfill({ json: { config: CONFIG, name: 'chunk-check' } }))

    await page.goto('/app/model-editor/chunk-check')
    await expect(page.getByTestId('rt-context_size')).toHaveValue('4096', { timeout: 15_000 })
    await expect(page.getByTestId('rt-preview-command')).toContainText('--ctx-size 4096')

    const styles = await page.evaluate(() => {
      const rt = getComputedStyle(document.querySelector('.rt'))
      const flag = getComputedStyle(document.querySelector('.rt-flag'))
      return { display: rt.display, container: rt.containerType, flagBackground: flag.backgroundColor }
    })
    expect(styles.display).toBe('grid')
    expect(styles.container).toBe('inline-size')
    expect(styles.flagBackground).not.toBe('rgba(0, 0, 0, 0)')
    expect(problems).toEqual([])
  })
})
