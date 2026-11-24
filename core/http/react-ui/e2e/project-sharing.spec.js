import { test, expect } from './coverage-fixtures.js'
import { SHARED, HOSTILE_MEMORY, EVIL, FOLDER_ID, OWNER_SLUG, LEAK_ID, LEAK_PATH, installSharing, mockFolderShares, mockProposals, mockShared, signIn, PROPOSAL } from './project-sharing-fixtures.js'
import { mockProjects } from './projects-fixtures.js'

// Sharing a project: the owner's Share dialog, the Shared with me list, the
// page of a shared project, pulling it into a chat, the pickers and chips of
// the chat, and the owner's proposal card. The API is stubbed; the Go specs
// cover the server. Set PROJECT_SHARING_SHOTS_DIR to save screenshots.

const SHOTS = process.env.PROJECT_SHARING_SHOTS_DIR
const shot = async (page, name) => { if (SHOTS) await page.screenshot({ path: `${SHOTS}/${name}.png` }) }
// The header and the narrow strip both draw the chips; one is hidden by CSS.
const pulledChips = (page) => page.locator('[data-testid="chat-pulled-chip"]:visible')
const sharedRow = (page, name) => page.getByTestId('shared-row').filter({ hasText: name })

// The text of the page must hold nothing of the owner's account or paths.
async function expectNoIds(page) {
  const html = await page.content()
  expect(html).not.toContain(LEAK_ID)
  expect(html).not.toContain(LEAK_PATH)
  expect(html).not.toContain('/users/')
}

test.describe('Share a project (owner)', () => {
  test('the Share button opens the dialog for the project folder and sets a role', async ({ page }) => {
    const s = await installSharing(page)
    await page.goto('/app/projects/p1')
    await expect(page.getByTestId('project-shared-count')).toHaveText('Shared with 1')
    await page.getByTestId('project-share').click()
    const dialog = page.getByTestId('project-share-dialog')
    await expect(dialog).toBeVisible()
    await expect(dialog).toContainText('Anyone you share with sees the whole project: files, memory and settings. People with edit access can change its files, memory and settings.')
    await expect(dialog).toContainText('Client site redesign')
    // The folder is found by its path, then the grants are read for that folder.
    expect(s.folderShares.found).toEqual([`/Projects/${OWNER_SLUG}`])
    await expect(dialog.getByText('Bob')).toBeVisible()
    await dialog.getByRole('combobox', { name: /Bob/ }).selectOption('write').catch(async () => {
      await dialog.locator('select').first().selectOption('write')
    })
    await expect.poll(() => s.folderShares.seen.filter(c => c.method === 'PUT').map(c => [c.path, c.body])).toEqual([[`/${FOLDER_ID}/grants/u-bob`, { role: 'write' }]])
    expect(s.folderShares.seen.find(c => c.method === 'PUT').confirm).toBe(String(FOLDER_ID))
    await shot(page, 'owner-share-dialog')
  })

  test('the dialog offers the read and edit roles and sharing with everyone', async ({ page }) => {
    await installSharing(page)
    await page.goto('/app/projects/p1')
    await page.getByTestId('project-share').click()
    const dialog = page.getByTestId('project-share-dialog')
    await expect(dialog.getByRole('button', { name: 'Share with everyone' })).toBeVisible()
    await expect(dialog.locator('select option[value="read"]').first()).toHaveText('Can read')
    await expect(dialog.locator('select option[value="write"]').first()).toHaveText('Can edit')
  })

  test('an archived project can still be shared', async ({ page }) => {
    await installSharing(page)
    await page.goto('/app/projects/p3')
    await expect(page.getByTestId('project-share')).toBeEnabled()
  })

  test('the button is not drawn where sharing is off or accounts are off', async ({ page }) => {
    const s = await mockProjects(page)
    await signIn(page)
    await mockShared(page, s)
    mockFolderShares(page, { sharing: false })
    await page.goto('/app/projects/p1')
    await expect(page.getByTestId('project-edit')).toBeVisible()
    await expect(page.getByTestId('project-share')).toHaveCount(0)
  })

  test('without accounts there is no Share button and no share call', async ({ page }) => {
    const s = await mockProjects(page)
    await signIn(page, { authEnabled: false })
    await mockShared(page, s)
    const shares = mockFolderShares(page)
    await page.goto('/app/projects/p1')
    await expect(page.getByTestId('project-edit')).toBeVisible()
    await expect(page.getByTestId('project-share')).toHaveCount(0)
    expect(shares.seen).toHaveLength(0)
  })
})

test.describe('Shared with me', () => {
  test('the list shows each shared project with its owner, access and state', async ({ page }) => {
    await installSharing(page)
    await page.goto('/app/projects')
    const section = page.getByTestId('projects-shared')
    await expect(section.getByRole('heading', { name: 'Shared with me' })).toBeVisible()
    await expect(page.getByTestId('shared-row')).toHaveCount(4)
    const brand = sharedRow(page, 'Brand guide')
    await expect(brand).toContainText('Shared by Dana Reyes')
    await expect(brand).toContainText('Read only')
    await expect(brand.getByRole('link', { name: 'Open Brand guide' })).toHaveAttribute('href', '/app/projects/shared/s%3A12')
    await expect(sharedRow(page, 'Launch checklist')).toContainText('Can edit files')
    const archived = sharedRow(page, 'Podcast episode 14')
    await expect(archived).toContainText('Unavailable')
    await expect(archived.getByTestId('shared-row-note')).toContainText('The owner archived this project')
    await expect(archived.getByRole('button', { name: 'Open Podcast episode 14' })).toBeDisabled()
    await expect(archived.getByRole('link')).toHaveCount(0)
    await expect(sharedRow(page, 'Old plan')).toContainText('Unavailable')
    await expectNoIds(page)
    await shot(page, 'projects-shared-list')
  })

  test('the section is hidden when nothing is shared', async ({ page }) => {
    await installSharing(page, { sharedOpts: { shared: [] } })
    await page.goto('/app/projects')
    await expect(page.getByTestId('project-row')).toHaveCount(4)
    await expect(page.getByTestId('projects-shared')).toHaveCount(0)
  })

  test('a failed shared read hides the section with no error and no toast', async ({ page }) => {
    const s = await installSharing(page)
    s.shared.fail = true
    await page.goto('/app/projects')
    await expect(page.getByTestId('project-row')).toHaveCount(4)
    await expect.poll(() => s.shared.calls.length).toBeGreaterThan(0)
    await expect(page.getByTestId('projects-shared')).toHaveCount(0)
    await expect(page.getByRole('alert')).toHaveCount(0)
    await expect(page.locator('.toast, [data-testid^="toast"]')).toHaveCount(0)
  })

  test('the empty state shows only when both lists are empty', async ({ page }) => {
    await installSharing(page, { projectsOpts: { projects: [] }, sharedOpts: { shared: [] } })
    await page.goto('/app/projects')
    await expect(page.getByTestId('projects-empty')).toBeVisible()
  })

  test('a person with no own project but a shared one sees the section, not the empty state', async ({ page }) => {
    await installSharing(page, { projectsOpts: { projects: [] } })
    await page.goto('/app/projects')
    await expect(page.getByTestId('projects-shared')).toBeVisible()
    await expect(page.getByTestId('projects-empty')).toHaveCount(0)
    await expect(page.getByTestId('project-new')).toHaveCount(1)
  })
})

test.describe('A shared project page', () => {
  test('a read share shows the banner, the two tabs and nothing to change', async ({ page }) => {
    await installSharing(page)
    await page.goto('/app/projects/shared/s%3A12')
    await expect(page.getByRole('heading', { name: 'Brand guide', level: 1 })).toBeVisible()
    const banner = page.getByTestId('shared-banner')
    await expect(banner).toContainText('Shared by Dana Reyes')
    await expect(banner).toContainText('read only')
    await expect(page.getByRole('tab')).toHaveCount(2)
    await expect(page.getByTestId('shared-tab-overview')).toHaveText('Overview')
    await expect(page.getByTestId('shared-tab-memory')).toHaveText('Memory')
    const facts = page.getByTestId('shared-facts')
    await expect(facts).toContainText('Dana Reyes')
    await expect(facts).toContainText('Read only')
    await expect(facts).toContainText('Files, memory and project settings')
    await expect(facts).toContainText('Read-only in a pulled chat')
    for (const name of ['Edit', 'Archive', 'Delete', 'Share', 'Start chat in this project']) {
      await expect(page.getByRole('button', { name, exact: true })).toHaveCount(0)
    }
    for (const tab of ['Agents', 'Connections', 'Automations', 'Tool defaults', 'Files']) await expect(page.getByRole('tab', { name: tab })).toHaveCount(0)
    await expectNoIds(page)
    await shot(page, 'shared-page-read')
  })

  test('a write share says the person can edit files', async ({ page }) => {
    await installSharing(page)
    await page.goto('/app/projects/shared/s%3A14')
    await expect(page.getByTestId('shared-banner')).toContainText('you can edit files')
    await expect(page.getByTestId('shared-facts')).toContainText('Can edit files')
    await expect(page.getByTestId('shared-facts')).toContainText('Read-only in a pulled chat')
    await shot(page, 'shared-page-write')
  })

  test('the Memory tab shows the index as text and the topic names, with no editing and no image fetch', async ({ page }) => {
    await installSharing(page)
    const images = []
    await page.route('https://tracker.example.test/**', (route) => { images.push(route.request().url()); route.abort() })
    await page.goto('/app/projects/shared/s%3A12')
    await page.getByTestId('shared-tab-overview').focus()
    await page.keyboard.press('ArrowRight')
    await expect(page.getByTestId('shared-tab-memory')).toHaveAttribute('aria-selected', 'true')
    const index = page.getByTestId('shared-memory-index')
    await expect(index).toContainText('Voice: plain, short sentences.')
    await expect(index.locator('img')).toHaveCount(0)
    await expect(page.getByTestId('shared-memory-file')).toHaveText(['brand-voice.md', 'logo-rules.md'])
    await expect(page.getByRole('button', { name: /Edit/ })).toHaveCount(0)
    await expect(page.locator('textarea')).toHaveCount(0)
    expect(images).toHaveLength(0)
    await shot(page, 'shared-page-memory')
  })

  test('an archived share is shown as unavailable and Pull into a chat is off', async ({ page }) => {
    await installSharing(page)
    await page.goto('/app/projects/shared/s%3A15')
    await expect(page.getByTestId('shared-unavailable')).toContainText('The owner archived this project.')
    await expect(page.getByTestId('shared-pull')).toBeDisabled()
    await expect(page.getByTestId('shared-use-body')).toContainText('You cannot pull this project into a chat while it is archived.')
  })

  test('a project that is not shared (any more) shows the not available state with a way back', async ({ page }) => {
    const s = await installSharing(page)
    s.shared.gone.add('s:12')
    await page.goto('/app/projects/shared/s%3A12')
    const missing = page.getByTestId('shared-project-missing')
    await expect(missing).toContainText('This project is not available')
    await missing.getByRole('link', { name: 'Back to projects' }).click()
    await expect(page).toHaveURL(/\/app\/projects$/)
  })

  test('a failed load shows the load error with Retry', async ({ page }) => {
    const s = await installSharing(page)
    s.shared.fail = true
    await page.goto('/app/projects/shared/s%3A12')
    await expect(page.getByText('Could not load this project.')).toBeVisible()
    s.shared.fail = false
    await page.getByRole('button', { name: /Retry|Try again/ }).click()
    await expect(page.getByRole('heading', { name: 'Brand guide', level: 1 })).toBeVisible()
  })

  test('pulling into a new chat creates the chat, then sends the exact ref', async ({ page }) => {
    const s = await installSharing(page)
    await page.goto('/app/projects/shared/s%3A12')
    await page.getByTestId('shared-pull').click()
    await page.getByRole('menuitem', { name: 'New chat' }).click()
    await expect(page).toHaveURL(/\/app\/chats\/c-new/)
    const order = s.calls.filter(c => c.method !== 'GET' && (c.path === '/api/chats' || c.path.endsWith('/projects')))
    expect(order.map(c => `${c.method} ${c.path}`)).toEqual(['POST /api/chats', 'PUT /api/chats/c-new/projects'])
    expect(order[0].body.project_id).toBeUndefined()
    expect(order[1].body).toEqual({ project_ids: ['s:12'] })
  })

  test('pulling into a recent chat keeps what it already pulled', async ({ page }) => {
    const s = await installSharing(page)
    s.listChats = true
    s.chats[0].pulled_projects = ['p2']
    await page.goto('/app/projects/shared/s%3A12')
    await page.getByTestId('shared-pull').click()
    await page.getByRole('menuitem', { name: 'Pricing page copy' }).click()
    await expect(page).toHaveURL(/\/app\/chats\/c1/)
    const put = s.calls.find(c => c.method === 'PUT' && c.path === '/api/chats/c1/projects')
    expect(put.body).toEqual({ project_ids: ['p2', 's:12'] })
  })
})

test.describe('Chat', () => {
  const pulledChat = (s, ids) => { s.chats[0].pulled_projects = ids }

  test('the chips of a pulled shared project say who shared it and what you can do; memory stays read-only', async ({ page }) => {
    const s = await installSharing(page)
    pulledChat(s, ['s:12', 's:14'])
    await page.goto('/app/chats/c1')
    const chips = pulledChips(page)
    await expect(chips).toHaveCount(2)
    const brand = chips.filter({ hasText: 'Brand guide' })
    await expect(brand).toContainText('Read only')
    await expect(brand).toContainText('Shared by Dana Reyes')
    await expect(brand).toHaveAttribute('href', '/app/projects/shared/s%3A12')
    await expect(chips.filter({ hasText: 'Launch checklist' })).toContainText('Can edit files')
    const note = page.getByTestId('chat-pulled-callout')
    await expect(note).toContainText('Launch checklist is shared by Sam Okafor')
    await expect(note).toContainText('notes are proposed and Sam Okafor confirms them')
    await expect(note).toContainText('ordinary shared folder')
    await expectNoIds(page)
    await shot(page, 'chat-pulled-chips')
  })

  test('a pulled ref that is not in the shared list shows as unavailable, with Retry and Remove', async ({ page }) => {
    const s = await installSharing(page)
    pulledChat(s, ['s:12', 's:99'])
    await page.goto('/app/chats/c1')
    await expect(pulledChips(page).filter({ hasText: 'Brand guide' })).toBeVisible()
    await expect(pulledChips(page).filter({ hasText: 'A shared project' })).toHaveAttribute('data-state', 'unlisted')
    const notice = page.getByTestId('chat-pulled-unlisted')
    await expect(notice).toContainText('not available now')
    await expect(notice).not.toContainText('no longer shared')
    await expect(notice.getByRole('button', { name: 'Retry' })).toBeVisible()
    await notice.getByRole('button', { name: 'Remove from chat' }).click()
    await expect.poll(() => s.calls.filter(c => c.method === 'PUT' && c.path === '/api/chats/c1/projects').map(c => c.body)).toEqual([{ project_ids: ['s:12'] }])
    await expect(page.getByTestId('chat-pulled-unlisted')).toHaveCount(0)
    await shot(page, 'chat-revoked')
  })

  test('a failed shared read never hides or breaks the chat', async ({ page }) => {
    const s = await installSharing(page)
    pulledChat(s, ['p2', 's:12'])
    s.shared.fail = true
    await page.goto('/app/chats/c1')
    await expect(pulledChips(page)).toHaveCount(1)
    await expect(pulledChips(page)).toContainText('Home lab notes')
    await expect(page.getByTestId('chat-pulled-unlisted')).toHaveCount(0)
  })

  test('chat settings: the pulled picker has a Shared with you group, the home select never lists shared projects', async ({ page }) => {
    const s = await installSharing(page)
    pulledChat(s, ['s:12'])
    await page.goto('/app/chats/c1')
    await page.getByRole('button', { name: 'Chat settings' }).click()
    const section = page.getByTestId('chat-project-settings')
    await expect(section.getByTestId('chat-pulled-row')).toContainText('Brand guide')
    await expect(section.getByTestId('chat-pulled-shared-badge')).toHaveText('Shared')
    await expect(section.getByTestId('chat-pulled-shared-hint')).toBeVisible()
    const home = section.locator('#cx-set-project')
    for (const name of ['Brand guide', 'Launch checklist', 'Podcast episode 14']) await expect(home.locator('option', { hasText: name })).toHaveCount(0)
    const add = section.getByLabel('Add a project')
    const group = add.locator('optgroup[label="Shared with you"]')
    await expect(group.locator('option')).toHaveText(['Launch checklist (shared by Sam Okafor)'])
    await add.selectOption('s:14')
    await section.getByRole('button', { name: 'Add', exact: true }).click()
    await expect.poll(() => s.calls.filter(c => c.method === 'PUT' && c.path === '/api/chats/c1/projects').map(c => c.body)).toEqual([{ project_ids: ['s:12', 's:14'] }])
    await shot(page, 'chat-settings')
  })

  test('the composer picker lists shared projects under Pull in; the start list never does', async ({ page }) => {
    await installSharing(page)
    await page.goto('/app')
    await page.getByTestId('project-picker-button').click()
    const menu = page.getByTestId('project-picker-menu')
    await expect(menu.getByTestId('project-picker-shared-head')).toHaveText('Pull in')
    const sharedOpts = menu.locator('[role="option"][data-shared="true"]')
    await expect(sharedOpts).toHaveCount(2)
    await expect(sharedOpts.first()).toContainText('Brand guide')
    await expect(sharedOpts.first()).toContainText('Shared')
    await expect(sharedOpts.first()).toContainText('Shared by'.length ? 'Dana Reyes' : '')
    await expect(menu).not.toContainText('Podcast episode 14')
    // The start list holds the own active projects only.
    const own = menu.locator('[role="option"]:not([data-shared="true"])')
    await expect(own.filter({ hasText: 'Brand guide' })).toHaveCount(0)
    await expect(own.filter({ hasText: 'Client site redesign' })).toHaveCount(1)
    await shot(page, 'composer-picker')
    await sharedOpts.first().click()
    await expect(page.getByTestId('project-picker-button')).toContainText('Pulled: 1')
    await expectNoIds(page)
  })
})

test.describe('Proposal from another person', () => {
  test('the card names the proposer, the project and where it came from', async ({ page }) => {
    await signIn(page)
    await mockProposals(page)
    await page.goto('/app/chats/proposals')
    const card = page.getByTestId('change-proposal')
    await expect(card.getByTestId('change-summary')).toHaveText('Dana Reyes proposed a note for Client site redesign')
    await expect(card).toContainText('Dana Reyes, from their own chat')
    await expect(card).toContainText('Proposed by')
    await expect(card.getByRole('button', { name: 'Confirm', exact: true })).toBeVisible()
    await expect(card.getByRole('button', { name: 'Edit then confirm' })).toBeVisible()
    await expect(card.getByRole('button', { name: 'Dismiss' })).toBeVisible()
    await shot(page, 'proposal-card')
  })

  test('a voided proposal answers 409: the server message shows and the card says it was not applied', async ({ page }) => {
    await signIn(page)
    const s = await mockProposals(page, { voided: true })
    await page.goto('/app/chats/proposals')
    await page.getByTestId('change-proposal').getByRole('button', { name: 'Confirm', exact: true }).click()
    const card = page.getByTestId('change-proposal')
    await expect(card).toHaveAttribute('data-state', 'stale')
    await expect(card).toContainText('Not applied:')
    await expect(card.getByTestId('change-voided')).toHaveText('Dana Reyes can no longer edit this project.')
    await expect(card.getByTestId('change-error')).toContainText('Dana Reyes can no longer edit this project (the grant ended). Nothing was written')
    await expect(card.getByRole('button', { name: 'Ask again' })).toHaveCount(0)
    expect(s.calls.some(c => c.path.endsWith('/confirm'))).toBe(true)
    await shot(page, 'proposal-voided')
  })

  test('the proposer is shown as text: no account or id reaches the page', async ({ page }) => {
    await signIn(page)
    await mockProposals(page, { proposal: { ...PROPOSAL, owner: LEAK_ID, project_node: 77 } })
    await page.goto('/app/chats/proposals')
    await expect(page.getByTestId('change-proposal')).toBeVisible()
    await expectNoIds(page)
  })
})

test.describe('Shared memory is the owner\'s text', () => {
  test('a hostile index makes no request to the owner\'s addresses and still shows its text', async ({ page }) => {
    await installSharing(page, { sharedOpts: { memory: HOSTILE_MEMORY } })
    const hits = []
    await page.route('**://evil.example/**', (route) => { hits.push(route.request().url()); route.abort() })
    await page.goto('/app/projects/shared/s%3A12?tab=memory')
    const index = page.getByTestId('shared-memory-index')
    await expect(index).toContainText('Safe text stays visible.')
    await expect(index).toContainText('Styled text stays visible.')
    await expect(index.locator('img, picture, source, video, svg, iframe, style, [style]')).toHaveCount(0)
    // The links are text only: no address to follow.
    await expect(index.locator('a[href]')).toHaveCount(0)
    await page.getByTestId('shared-tab-overview').click()
    await page.waitForTimeout(500)
    expect(hits).toEqual([])
    expect(await page.content()).not.toContain(EVIL)
  })

  test('a failed memory read says so, with Retry, and is not shown as an empty memory', async ({ page }) => {
    const s = await installSharing(page)
    s.shared.memoryFail = true
    await page.goto('/app/projects/shared/s%3A12?tab=memory')
    await expect(page.getByTestId('shared-memory-failed')).toContainText('Could not load the memory of this project.')
    await expect(page.getByText('The index is empty.')).toHaveCount(0)
    s.shared.memoryFail = false
    await page.getByTestId('shared-memory-failed').getByRole('button', { name: /Try again|Retry/ }).click()
    await expect(page.getByTestId('shared-memory-index')).toContainText('Voice: plain')
  })

  test('another ref starts clean: nothing of the first project stays', async ({ page }) => {
    await installSharing(page)
    await page.goto('/app/projects/shared/s%3A12')
    await expect(page.getByRole('heading', { name: 'Brand guide', level: 1 })).toBeVisible()
    await page.evaluate(() => { history.pushState({}, '', '/app/projects/shared/s%3A14'); window.dispatchEvent(new PopStateEvent('popstate')) })
    await expect(page.getByRole('heading', { name: 'Launch checklist', level: 1 })).toBeVisible()
    await expect(page.getByTestId('shared-banner')).toContainText('Sam Okafor')
    await expect(page.getByText('Brand guide')).toHaveCount(0)
  })

  test('when pulling fails after the chat is created the chat opens and the toast says what to do', async ({ page }) => {
    const s = await installSharing(page)
    s.shared.pullFail = true
    await page.goto('/app/projects/shared/s%3A12')
    await page.getByTestId('shared-pull').click()
    await page.getByRole('menuitem', { name: 'New chat' }).click()
    await expect(page).toHaveURL(/\/app\/chats\/c-new/)
    await expect(page.getByText('Open the chat settings to pull it in.')).toBeVisible()
  })
})

test.describe('Chat header chips', () => {
  test('more than two pulled projects collapse into +N, and the settings list every one', async ({ page }) => {
    const s = await installSharing(page)
    s.chats[0].pulled_projects = ['p2', 's:12', 's:14', 's:15']
    await page.goto('/app/chats/c1')
    await expect(pulledChips(page)).toHaveCount(2)
    const more = page.locator('[data-testid="chat-pulled-more"]:visible')
    await expect(more).toHaveText('+2')
    await expect(more).toHaveAttribute('title', /2 more pulled projects/)
    // The header never clips: the chips row stays inside the header.
    const box = await page.getByTestId('chat-project-chips').boundingBox()
    expect(box.y).toBeGreaterThanOrEqual(0)
    await more.click()
    const rows = page.getByTestId('chat-project-settings').getByTestId('chat-pulled-row')
    await expect(rows).toHaveCount(4)
    await expect(rows.nth(3)).toContainText('Podcast episode 14')
  })

  test('two pulled projects or fewer have no +N', async ({ page }) => {
    const s = await installSharing(page)
    s.chats[0].pulled_projects = ['p2', 's:12']
    await page.goto('/app/chats/c1')
    await expect(pulledChips(page)).toHaveCount(2)
    await expect(page.getByTestId('chat-pulled-more')).toHaveCount(0)
  })
})

test.describe('The owner learns of a note from another person', () => {
  // The inbox row counts the same pending list (chats-proposals-inbox.spec.js
  // covers the row); here the list has a row with a proposer.
  test('a pending row with a proposer is drawn on the Proposals page as the owner\'s card', async ({ page }) => {
    await installSharing(page)
    await mockProposals(page)
    await page.goto('/app/chats/proposals')
    await expect(page.getByTestId('change-summary')).toHaveText('Dana Reyes proposed a note for Client site redesign')
    await expect(page.getByTestId('change-summary')).toHaveAttribute('title', 'Project name taken from its folder')
    await expect(page.getByTestId('change-rows')).toContainText('Projects/client-site-redesign/memory/brand-voice.md')
  })

  test('the project page lists the notes waiting for its memory, with a link to Proposals', async ({ page }) => {
    await installSharing(page)
    await mockProposals(page)
    await page.goto('/app/projects/p1')
    const waiting = page.getByTestId('project-waiting')
    await expect(waiting).toContainText('Waiting for you')
    await expect(waiting).toContainText('1')
    await waiting.getByTestId('project-waiting-link').click()
    await expect(page).toHaveURL(/\/app\/chats\/proposals$/)
  })

  test('a project with no waiting notes shows no block', async ({ page }) => {
    await installSharing(page)
    await mockProposals(page, { proposal: { ...PROPOSAL, target: 'Projects/other-project/memory/x.md' } })
    await page.goto('/app/projects/p1')
    await expect(page.getByTestId('project-overview')).toBeVisible()
    await expect(page.getByTestId('project-waiting')).toHaveCount(0)
  })
})
