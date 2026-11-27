import { test, expect } from './coverage-fixtures.js'
import { mockUser } from './home-fixtures.js'
import { INSTALLED, SHA, stubPlugins as stub } from './plugins-fixtures.js'

// The Plugins tab of the Add-ons page with the plugin API stubbed: browse
// cards with what a plugin adds, the review before an install (a plugin with
// no code, and one with code that needs a tick and the confirm header), the
// switch that answers needs_reenable, the update review with a tick per code
// file, and the marketplaces.

test.describe('Plugins tab', () => {
  test('browse shows what each plugin adds, the code marks and one control', async ({ page }) => {
    await stub(page)
    await page.goto('/app/addons?tab=plugins')
    await expect(page.getByTestId('plugins-browse')).toBeVisible()
    const cards = page.getByTestId('plugin-card')
    await expect(cards).toHaveCount(4)
    const lint = cards.filter({ hasText: 'lint-hooks' })
    // Whether a plugin runs code is unknown until a version was reviewed or
    // installed: the card says so and claims neither.
    const notes = cards.filter({ hasText: 'notes-kit' })
    await expect(notes.getByTestId('plugin-unchecked')).toHaveText('Not checked yet')
    await expect(notes.getByTestId('plugin-no-code')).toHaveCount(0)
    await expect(notes.getByTestId('plugin-runs-code')).toHaveCount(0)
    await expect(cards.filter({ hasText: 'docs-helper' }).getByTestId('plugin-no-code')).toHaveText('No code')
    await expect(lint.getByTestId('plugin-runs-code')).toHaveText('Runs code')
    await expect(lint.getByTestId('plugin-needs-sandbox')).toHaveText('Needs Sandbox')
    await expect(cards.filter({ hasText: 'docs-helper' })).toContainText('Curated by LocalAI')
    await expect(cards.filter({ hasText: 'npm-thing' }).getByTestId('plugin-unsupported')).toHaveText('npm sources are not supported')
    // The filter hides the plugin known to run code; an unchecked one stays.
    await page.getByTestId('plugins-only-text').click()
    await expect(cards).toHaveCount(3)
    await page.getByTestId('plugins-only-all').click()
    await page.getByTestId('plugins-search').fill('docs')
    await expect(cards).toHaveCount(1)
  })

  test('a user sees Install only on a plugin with no code', async ({ page }) => {
    await mockUser(page, { plugins: true, chats: true })
    await stub(page)
    await page.goto('/app/addons?tab=plugins')
    const cards = page.getByTestId('plugin-card')
    await expect(cards).toHaveCount(4)
    await expect(cards.filter({ hasText: 'docs-helper' }).getByTestId('plugin-install')).toBeVisible()
    await expect(cards.filter({ hasText: 'lint-hooks' }).getByTestId('plugin-install')).toHaveCount(0)
    await expect(cards.filter({ hasText: 'lint-hooks' }).getByTestId('plugin-admin-only')).toHaveText('This plugin runs code. An administrator can install it.')
    // An unchecked plugin can be reviewed; when the review finds code, the
    // badge becomes definite and the user gets no Install button.
    await cards.filter({ hasText: 'notes-kit' }).getByTestId('plugin-install').click()
    const dialog = page.getByTestId('plugin-install-review')
    await expect(dialog.getByTestId('plugin-review-admin-only')).toBeVisible()
    await expect(dialog.getByTestId('plugin-install-confirm')).toHaveCount(0)
    await dialog.getByRole('button', { name: 'Cancel' }).last().click()
    await expect(cards.filter({ hasText: 'notes-kit' }).getByTestId('plugin-runs-code')).toHaveText('Runs code')
    await expect(cards.filter({ hasText: 'notes-kit' }).getByTestId('plugin-unchecked')).toHaveCount(0)
  })

  test('install of a plugin with no code: review, then one POST with the token and no confirm header', async ({ page }) => {
    const api = await stub(page)
    await page.goto('/app/addons?tab=plugins')
    await page.getByTestId('plugin-card').filter({ hasText: 'docs-helper' }).getByTestId('plugin-install').click()
    const dialog = page.getByTestId('plugin-install-review')
    await expect(dialog).toBeVisible()
    await expect(dialog.getByTestId('plugin-review-unsigned')).toContainText('Community plugin, not signed')
    await expect(dialog.getByTestId('plugin-review-sha')).toHaveText(SHA.slice(0, 7))
    await expect(dialog.getByTestId('plugin-adds')).toContainText('2 skills')
    await expect(dialog.getByTestId('plugin-adds')).toContainText('Connects to docs.example.com')
    await expect(dialog.getByTestId('plugin-components')).toContainText('/docs-helper:outline')
    await expect(dialog.getByTestId('plugin-review-code')).toHaveCount(0)
    // The digest, one long word, wraps inside the dialog.
    const digest = dialog.getByTestId('plugin-digest')
    await expect(digest).toContainText('sha256:')
    const over = await dialog.evaluate((el) => {
      const d = el.querySelector('[data-testid="plugin-digest"]').getBoundingClientRect()
      const box = el.getBoundingClientRect()
      return d.right - box.right
    })
    expect(over).toBeLessThanOrEqual(0)
    // An administrator chooses the scope; everyone is the first choice.
    await dialog.getByTestId('plugin-scope-user').click()
    await dialog.getByTestId('plugin-install-confirm').click()
    await expect(dialog).toHaveCount(0)
    expect(api.posts).toEqual([{ body: { token: 'tok-docs-helper', scope: 'user' }, confirm: '' }])
  })

  test('install of a plugin with code: the files read as text, a tick, and the confirm header', async ({ page }) => {
    const api = await stub(page)
    await page.goto('/app/addons?tab=plugins')
    await page.getByTestId('plugin-card').filter({ hasText: 'lint-hooks' }).getByTestId('plugin-install').click()
    const dialog = page.getByTestId('plugin-install-review')
    await expect(dialog.getByTestId('plugin-review-code')).toContainText('This plugin runs code')
    const file = dialog.getByTestId('plugin-code-file').first()
    await expect(file).toHaveAttribute('data-path', 'hooks/hooks.json')
    // The file is text: its markup shows as characters, never as an element.
    await expect(file.getByTestId('plugin-code-file-text')).toContainText('echo "<b>not html</b>"')
    await expect(file.locator('b')).toHaveCount(0)
    await expect(dialog.getByTestId('plugin-review-scope')).toContainText('installed for everyone')
    const install = dialog.getByTestId('plugin-install-confirm')
    await expect(install).toBeDisabled()
    await dialog.getByTestId('plugin-review-read').check()
    await install.click()
    await expect(dialog).toHaveCount(0)
    expect(api.posts).toEqual([{ body: { token: 'tok-lint-hooks', scope: 'instance' }, confirm: 'lint-hooks@acme' }])
  })

  test('the review link of the CLI opens the review of that plugin once', async ({ page }) => {
    await stub(page)
    await page.goto('/app/addons?tab=plugins&plugin=lint-hooks%40acme')
    const dialog = page.getByTestId('plugin-install-review')
    await expect(dialog).toBeVisible()
    await expect(dialog).toContainText('Install lint-hooks')
    await expect(dialog.getByTestId('plugin-review-code')).toBeVisible()
    await expect(page).toHaveURL(/tab=plugins/)
    await expect(page).not.toHaveURL(/plugin=/)
  })

  test('a switch that answers needs_reenable asks, then sends reenable', async ({ page }) => {
    const api = await stub(page, { installs: INSTALLED.map((i) => ({ ...i })) })
    await page.goto('/app/addons?tab=plugins&view=installed')
    const row = page.getByTestId('plugin-row').filter({ hasText: 'lint-hooks' })
    await expect(row.getByTestId('plugin-row-reenable')).toContainText('This plugin now runs code in your Sandbox')
    await row.getByTestId('plugin-row-switch').click()
    const confirm = page.getByRole('alertdialog')
    await expect(confirm).toContainText('This plugin now runs code in your Sandbox.')
    await confirm.getByRole('button', { name: 'Turn on again' }).click()
    await expect.poll(() => api.mePuts.length).toBe(2)
    expect(api.mePuts).toEqual([{ enabled: true }, { enabled: true, reenable: true }])
    await expect(row.getByTestId('plugin-row-reenable')).toHaveCount(0)
  })

  test('the update review asks a tick for each changed code file and sends them', async ({ page }) => {
    const api = await stub(page, { installs: INSTALLED.map((i) => ({ ...i })) })
    await page.goto('/app/addons?tab=plugins&view=installed')
    await page.getByTestId('plugin-row').filter({ hasText: 'lint-hooks' }).getByTestId('plugin-row-update').click()
    const dialog = page.getByTestId('plugin-update-review')
    await expect(dialog.getByTestId('plugin-update-files')).toContainText('hooks/lint.sh')
    await expect(dialog.getByTestId('plugin-diff')).toContainText('+eslint --fix .')
    const go = dialog.getByTestId('plugin-update-confirm')
    await expect(go).toBeDisabled()
    await dialog.getByTestId('plugin-update-tick').check()
    await go.click()
    await expect(dialog).toHaveCount(0)
    expect(api.updates).toEqual([{ body: { token: 'up-1', approved_files: ['hooks/lint.sh'] }, confirm: 'in-code' }])
  })

  test('the installed details list what it adds and the marketplaces show their state', async ({ page }) => {
    const api = await stub(page, { installs: INSTALLED.map((i) => ({ ...i })) })
    await page.goto('/app/addons?tab=plugins&view=installed')
    await expect(page.getByTestId('plugins-group-instance')).toContainText('For everyone')
    await page.getByTestId('plugin-row').filter({ hasText: 'docs-helper' }).locator('.ad-tile__open').click()
    const pane = page.getByTestId('plugin-details')
    await expect(pane).toContainText('docs-helper:review')
    await page.getByTestId('plugins-view-marketplaces').click()
    await expect(page.getByTestId('plugins-marketplace-row')).toHaveCount(2)
    await expect(page.getByTestId('plugins-marketplace-curated')).toHaveText('Curated by LocalAI')
    await page.getByTestId('plugins-marketplace-add').click()
    await page.getByTestId('plugins-marketplace-source').fill('team/plugins')
    await page.getByTestId('plugins-marketplace-save').click()
    await expect.poll(() => api.added).toEqual([{ source: 'team/plugins' }])
  })
})
