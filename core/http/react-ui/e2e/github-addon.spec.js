import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi, openPane } from './connector-fixtures.js'

// The page of the GitHub add-on (the real built bundle, inside the add-on
// shell) with the core API stubbed: the token help, the repository list and
// its errors, the write mode cards, and the Test result drawn in the row.

const PAGE = '/app/addons/github/apps'
const BINDING = { id: 'b1', name: 'Docs repo', kind: 'github', agent_id: 'a1', enabled: false, accept_any: false, allow: [], resources: ['acme/widgets', 'acme/docs'], write_mode: 'propose', has_token: true }
const KIND = { capabilities: [], kindExtra: { title: 'GitHub', files: undefined } }
const api = (page, initial = []) => connectorApi(page, initial, 'github', [], undefined, KIND)

// The check tool answers through the add-on gateway of the page.
const checkAnswer = (answer) => (body) => (body.arguments?.tool === 'check'
  ? { status: 200, contentType: 'application/json', body: JSON.stringify({ payload: { data: answer(body.arguments.repo) } }) }
  : null)

test.describe('GitHub add-on', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })
  const install = (page, answer = () => ({ reachable: true })) => installStudioAddons(page, { ids: ['github'], dispatch: checkAnswer(answer) })

  test('a first connection sees the token help open, a link to GitHub and the permission table', async ({ page }) => {
    await install(page)
    await api(page).install()
    await page.goto(PAGE)
    await expect(page.getByTestId('github-limits')).toContainText('does not answer @mentions')
    await expect(page.getByTestId('github-setup')).toHaveAttribute('open', '')
    const link = page.getByTestId('github-token-link').first()
    await expect(link).toHaveAttribute('href', 'https://github.com/settings/personal-access-tokens/new')
    await expect(link).toHaveAttribute('target', '_blank')
    await expect(page.getByTestId('github-perms')).toContainText('Pull requests')
    await expect(page.getByTestId('github-perms')).toContainText('Read and write')
  })

  test('with a connection the help is closed', async ({ page }) => {
    await install(page)
    await api(page, [BINDING]).install()
    await page.goto(PAGE)
    await expect(page.getByTestId('connector-row')).toContainText('Docs repo')
    await expect(page.getByTestId('github-setup')).not.toHaveAttribute('open', '')
  })

  test('errors sit under their field, the bad line is named and the name follows the first repository', async ({ page }) => {
    await install(page)
    await api(page).install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'New GitHub connection' }).click()
    await page.getByLabel('Fine-grained access token').fill('ghp_classic')
    await expect(page.getByLabel('Fine-grained access token')).toHaveAttribute('aria-invalid', 'true')
    await expect(page.getByLabel('Fine-grained access token')).toHaveAccessibleDescription(/starts with github_pat_/)
    await page.getByLabel('Repositories the agent may use').fill('acme/widgets\nnot a repo')
    await expect(page.getByLabel('Repositories the agent may use')).toHaveAccessibleDescription(/"not a repo" is not written owner\/name/)
    await expect(page.getByLabel('Name', { exact: true })).toHaveValue('GitHub: acme/widgets')
    await page.getByLabel('Name', { exact: true }).fill('Mine')
    await page.getByLabel('Repositories the agent may use').fill('acme/tools')
    await expect(page.getByLabel('Name', { exact: true })).toHaveValue('Mine')
    await expect(page.getByTestId('connector-save')).toBeDisabled()
  })

  test('each write mode says what it does, and the default is read only', async ({ page }) => {
    await install(page)
    await api(page).install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'New GitHub connection' }).click()
    const group = page.getByRole('radiogroup', { name: 'What the agent may change on GitHub' })
    await expect(group.getByRole('radio', { name: /Read only/ })).toBeChecked()
    await expect(group).toContainText('Each change waits for your OK.')
    await expect(group).toContainText('Runs at once')
    await expect(group).not.toContainText('Allowed')
  })

  test('the Test result is drawn per repository inside the row, with a tone, and a new run replaces it', async ({ page }) => {
    let n = 0
    await install(page, (repo) => {
      n++
      return repo === 'acme/docs' ? { reachable: false } : { reachable: true, fullName: repo, canWrite: n > 2 }
    })
    await api(page, [BINDING]).install()
    await page.goto(PAGE)
    await page.getByTestId('connector-test').click()
    await openPane(page)
    const row = page.getByTestId('connector-pane')
    await expect(row.getByTestId('github-checks')).toContainText('acme/widgets: reachable, read only.')
    await expect(row.getByTestId('github-checks')).toContainText('acme/docs: this token cannot reach it')
    await expect(row.locator('.gh-check--warn')).toHaveCount(1)
    await expect(row.locator('.gh-check--ok')).toHaveCount(1)
    await expect(page.getByText('Connected as support_bot.').first()).toBeVisible()
    await page.getByTestId('connector-test').click()
    await expect(row.getByTestId('github-checks')).toContainText('acme/widgets: reachable, and the token can write.')
  })
})
