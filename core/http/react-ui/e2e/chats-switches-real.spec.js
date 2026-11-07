import { test, expect } from './coverage-fixtures.js'
import { startChat } from './journey-fixtures.js'

// The skill switches of a chat name a skill by its id, and the pages of an
// add-on say why they are missing when add-ons are off. Against the real test
// server (add-ons off there), reading the API after each step. Skills the host
// grants with the memory notes are locked rows: they show their state and take
// no part in the switches or the bulk actions, so the spec leaves them out.
// Skills of a tool group the chat does not have (the admin recipes while the
// admin group is off) are listed as off, and the picker stores them off with
// every change, so the spec reads them as part of the list that is sent.

test.describe.configure({ timeout: 90_000 })

const api = (request, path) => request.get(path).then(r => r.json())

test('switching a skill off in a chat sends its id, and the chat lists it off', async ({ page }) => {
  const id = await startChat(page, 'skills please')
  const skills = (await api(page.request, `/api/chats/skills?chat=${id}`)).skills.filter(s => !s.shadowed && !s.invalid && !s.locked)
  // The skills the person can switch are the ones that are on; the others are
  // off because their tool group is, and stay off whatever the switches say.
  const switchable = skills.filter(s => s.enabled)
  const alreadyOff = skills.filter(s => !s.enabled).map(s => s.id)
  const skill = switchable[0]
  expect(skill.id).toBe(`${skill.layer}:${skill.name}`)
  await page.getByTestId('chat-skills-button').click()
  const menu = page.getByTestId('chat-skills-menu')
  const enabled = async () => (await api(page.request, `/api/chats/skills?chat=${id}`)).skills.filter(s => !s.shadowed && !s.invalid && !s.locked && switchable.some(w => w.id === s.id))
  const putOff = () => page.waitForRequest(r => r.method() === 'PUT' && r.url().endsWith(`/api/chats/${id}/skills`))

  // One switch per skill, named by the skill. Switching it sends its id.
  const one = menu.getByRole('switch', { name: skill.name, exact: true })
  await expect(one).toHaveAttribute('aria-checked', 'true')
  let put = putOff()
  await one.click()
  expect(JSON.parse((await put).postData()).off).toEqual([...alreadyOff, skill.id].sort())
  await expect(one).toHaveAttribute('aria-checked', 'false')
  await expect.poll(async () => (await enabled()).find(s => s.id === skill.id).enabled).toBe(false)

  // Disable all sends every id; Select all sends an empty list.
  put = putOff()
  await menu.getByRole('button', { name: 'Disable all' }).click()
  expect(JSON.parse((await put).postData()).off).toEqual(skills.map(s => s.id).sort())
  await expect.poll(async () => (await enabled()).every(s => !s.enabled)).toBe(true)
  await expect(menu.getByRole('button', { name: 'Disable all' })).toBeDisabled()
  put = putOff()
  await menu.getByRole('button', { name: 'Select all' }).click()
  expect(JSON.parse((await put).postData()).off).toEqual([])
  await expect.poll(async () => (await enabled()).every(s => s.enabled)).toBe(true)
  await expect(one).toHaveAttribute('aria-checked', 'true')
})

test('switching the files group off in a chat stores it, and Select all puts it back', async ({ page }) => {
  const id = await startChat(page, 'files please')
  const group = async (name) => (await api(page.request, `/api/chats/${id}/tools`)).builtins.find(b => b.group === name)
  expect((await group('files')).off).toBe(false)
  expect((await group('subagents')).off).toBe(false)
  await page.getByTestId('chat-tools-button').click()
  const menu = page.getByTestId('chat-tools-menu')
  const files = menu.getByRole('switch', { name: 'Your files', exact: true })
  await expect(files).toHaveAttribute('aria-checked', 'true')
  const putBuiltins = () => page.waitForRequest(r => r.method() === 'PUT' && r.url().endsWith(`/api/chats/${id}/builtins`))
  let put = putBuiltins()
  await files.click()
  expect(JSON.parse((await put).postData()).off).toEqual(['files'])
  await expect(files).toHaveAttribute('aria-checked', 'false')
  await expect.poll(async () => (await group('files')).off).toBe(true)
  put = putBuiltins()
  await menu.getByRole('button', { name: 'Select all' }).first().click()
  expect(JSON.parse((await put).postData()).off).toEqual([])
  await expect.poll(async () => (await group('files')).off).toBe(false)
})

test('an add-on address with add-ons off lands on Home with a notice, without package-specific promotion', async ({ page, request }) => {
  const features = await (await request.get('/api/features')).json()
  test.skip(features.addons !== false, 'needs a server with add-ons off')
  await page.goto('/app/addons/studio/studio/images')
  await page.waitForURL(/\/app$/)
  const note = page.getByTestId('home-addons-off')
  await expect(note).toContainText('Add-ons are off on this server')
  await expect(note).toContainText('That page is an add-on')
  // The Home blocks fade in; let them settle before the picture.
  await page.waitForTimeout(1000)
  await page.screenshot({ path: 'test-results/wiring-addons-off.png' })
  await note.getByRole('button', { name: 'Dismiss' }).click()
  await expect(note).toHaveCount(0)

  await page.getByRole('combobox', { name: 'Message' }).fill('/studio')
  await expect(page.getByRole('option')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Install Studio' })).toHaveCount(0)
})
