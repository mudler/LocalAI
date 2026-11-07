import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi, openEdit, openPane } from './connector-fixtures.js'

// The page of the Mail add-on (the real built bundle, inside the add-on shell)
// with the core API stubbed: the setup help, the connection fields and their
// errors, the protection choices, the write mode cards, the sealed record that
// a save sends, and the Test result for the two logins.

const PAGE = '/app/addons/mail/apps'
const BINDING = { id: 'b1', name: 'Mail: ann@example.com', kind: 'mail', agent_id: '', enabled: false, accept_any: false, allow: [], resources: ['ann@example.com'], write_mode: 'propose', has_token: true }
const KIND = { capabilities: [], kindExtra: { title: 'Mail', files: undefined, agent_optional: true, tool_only: true } }
const api = (page, initial = []) => connectorApi(page, initial, 'mail', [], undefined, KIND)

// The check tool answers through the add-on gateway of the page.
const checkAnswer = (answer) => (body) => (body.arguments?.tool === 'check'
  ? { status: 200, contentType: 'application/json', body: JSON.stringify({ payload: { data: answer(body.arguments.mailbox) } }) }
  : null)

const fill = async (page) => {
  await page.getByLabel('Mailbox address').fill('ann@example.com')
  await page.getByLabel('Password').fill('app-password')
  await page.getByTestId('mail-imap-host').fill('imap.example.com')
  await page.getByTestId('mail-smtp-host').fill('smtp.example.com')
}

test.describe('Mail add-on', () => {
  test.beforeEach(async ({ page }) => {
    await mockChat(page)
  })
  const install = (page, answer = () => ({ imap: { ok: true }, smtp: { ok: true } })) => installStudioAddons(page, { ids: ['mail'], dispatch: checkAnswer(answer) })

  test('a first connection sees the setup help open and the limits', async ({ page }) => {
    await install(page)
    await api(page).install()
    await page.goto(PAGE)
    await expect(page.getByTestId('mail-limits')).toContainText('Everything in the mailbox is reachable')
    await expect(page.getByTestId('mail-setup')).toHaveAttribute('open', '')
    await expect(page.getByTestId('mail-setup')).toContainText('app password')
  })

  test('with a connection the help is closed and the row shows the address and the mode', async ({ page }) => {
    await install(page)
    await api(page, [BINDING]).install()
    await page.goto(PAGE)
    await openPane(page)
    const row = page.getByTestId('connector-pane')
    await expect(row).toContainText('Mail: ann@example.com')
    await expect(row.getByTestId('mail-row-address')).toHaveText('ann@example.com')
    await expect(row.getByTestId('mail-row-write')).toHaveText('Asks you first')
    await expect(page.getByTestId('mail-setup')).not.toHaveAttribute('open', '')
  })

  test('errors sit under their field, the protection moves the port, and the name follows the address', async ({ page }) => {
    await install(page)
    await api(page).install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'New mail connection' }).click()
    await page.getByLabel('Mailbox address').fill('ann@example.com')
    await expect(page.getByLabel('Name', { exact: true })).toHaveValue('Mail: ann@example.com')
    await expect(page.getByLabel('Username')).toHaveValue('ann@example.com')
    await page.getByTestId('mail-imap-host').fill('https://imap.example.com')
    await expect(page.getByTestId('mail-imap-host')).toHaveAttribute('aria-invalid', 'true')
    await expect(page.getByTestId('mail-imap-host')).toHaveAccessibleDescription(/server name only/)
    await page.getByTestId('mail-imap-host').fill('imap.example.com')
    await page.getByTestId('mail-smtp-security').selectOption('starttls')
    await expect(page.getByTestId('mail-smtp-port')).toHaveValue('587')
    await page.getByTestId('mail-smtp-port').fill('465')
    await expect(page.getByTestId('mail-smtp-port')).toHaveAccessibleDescription(/match/)
    await page.getByLabel('Name', { exact: true }).fill('Mine')
    await page.getByLabel('Mailbox address').fill('bob@example.com')
    await expect(page.getByLabel('Name', { exact: true })).toHaveValue('Mine')
    await expect(page.getByTestId('connector-save')).toBeDisabled()
  })

  test('no protection warns and needs a confirmation before it can be saved', async ({ page }) => {
    await install(page)
    await api(page).install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'New mail connection' }).click()
    await fill(page)
    await expect(page.getByTestId('connector-save')).toBeEnabled()
    await page.getByTestId('mail-imap-security').selectOption('none')
    await expect(page.getByTestId('mail-imap-port')).toHaveValue('143')
    await expect(page.getByTestId('mail-imap-plain')).toContainText('plain text')
    await expect(page.getByTestId('connector-save')).toBeDisabled()
    await page.getByTestId('mail-plain-ok').check()
    await expect(page.getByTestId('connector-save')).toBeEnabled()
  })

  test('each write mode says what it does, and the default is read only', async ({ page }) => {
    await install(page)
    await api(page).install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'New mail connection' }).click()
    const group = page.getByRole('radiogroup', { name: 'What the agent may do with mail' })
    await expect(group.getByRole('radio', { name: /Read only/ })).toBeChecked()
    await expect(group).toContainText('Each send and each draft waits for your OK.')
    await expect(group.getByRole('radio')).toHaveCount(2)
  })

  test('a save sends the address as the resource and the whole connection sealed as the token', async ({ page }) => {
    await install(page)
    const a = api(page)
    await a.install()
    await page.goto(PAGE)
    await page.getByRole('button', { name: 'New mail connection' }).click()
    await fill(page)
    await page.getByLabel('Folders the agent may use').fill('INBOX\nArchive')
    await page.getByRole('radio', { name: /Asks you first/ }).check()
    await page.getByTestId('connector-save').click()
    await expect(page.getByTestId('connector-row')).toContainText('Mail: ann@example.com')
    const post = a.s.calls.find(c => c.method === 'POST' && c.url === '/api/chats/connectors')
    expect(post.body.kind).toBe('mail')
    expect(post.body.resources).toEqual(['ann@example.com'])
    expect(post.body.write_mode).toBe('propose')
    const rec = JSON.parse(post.body.token)
    expect(rec).toMatchObject({ v: 1, address: 'ann@example.com', username: 'ann@example.com', password: 'app-password', imap: { host: 'imap.example.com', port: 993, security: 'tls' }, smtp: { host: 'smtp.example.com', port: 465, security: 'tls' }, folders: ['INBOX', 'Archive'] })
  })

  test('editing a saved connection keeps the sealed details unless they are replaced', async ({ page }) => {
    await install(page)
    const a = api(page, [BINDING])
    await a.install()
    await page.goto(PAGE)
    await openEdit(page)
    await expect(page.getByTestId('mail-saved')).toContainText('ann@example.com')
    await expect(page.getByTestId('mail-imap')).toHaveCount(0)
    await page.getByRole('radio', { name: /Read only/ }).check()
    await page.getByTestId('connector-save').click()
    const put = a.s.calls.find(c => c.method === 'PUT' || (c.method === 'PATCH'))
    expect(put.body.token).toBeUndefined()
    expect(put.body.write_mode).toBe('off')
    expect(put.body.resources).toEqual(['ann@example.com'])
  })

  test('the Test names the login that failed, in the row', async ({ page }) => {
    await install(page, () => ({ imap: { ok: true }, smtp: { ok: false, error: 'The mail server refused the username or password for sending.' } }))
    await api(page, [BINDING]).install()
    await page.goto(PAGE)
    await page.getByTestId('connector-test').click()
    await openPane(page)
    const row = page.getByTestId('connector-pane')
    await expect(row.getByTestId('mail-check-imap')).toContainText('the login works')
    await expect(row.getByTestId('mail-check-smtp')).toContainText('Sending mail (SMTP) failed. The mail server refused the username or password')
    await expect(row.locator('.ml-check--warn')).toHaveCount(1)
    await expect(row.locator('.ml-check--ok')).toHaveCount(1)
  })
})
