import { test, expect } from './coverage-fixtures.js'
import { startChat } from './journey-fixtures.js'

// The skill switches of a chat name a skill by its id, and the pages of an
// add-on say why they are missing when add-ons are off. Against the real test
// server (add-ons off there), reading the API after each step.

test.describe.configure({ timeout: 90_000 })

const api = (request, path) => request.get(path).then(r => r.json())

test('switching a skill off in a chat sends its id, and the chat lists it off', async ({ page }) => {
  const id = await startChat(page, 'skills please')
  const skills = (await api(page.request, `/api/chats/skills?chat=${id}`)).skills.filter(s => !s.shadowed && !s.invalid)
  const skill = skills[0]
  expect(skill.id).toBe(`${skill.layer}:${skill.name}`)
  await page.getByTestId('chat-skills-button').click()
  const put = page.waitForRequest(r => r.method() === 'PUT' && r.url().endsWith(`/api/chats/${id}/skills`))
  await page.getByTestId('chat-skills-menu').getByRole('checkbox', { name: skill.name, exact: true }).uncheck()
  expect(JSON.parse((await put).postData()).off).toEqual([skill.id])
  await expect.poll(async () => (await api(page.request, `/api/chats/skills?chat=${id}`)).skills.find(s => s.id === skill.id).enabled).toBe(false)
})

test('an add-on address with add-ons off lands on Home with a notice, and /studio offers Install Studio', async ({ page, request }) => {
  const features = await (await request.get('/api/features')).json()
  test.skip(features.addons !== false, 'needs a server with add-ons off')
  await page.goto('/app/image')
  await page.waitForURL(/\/app$/)
  const note = page.getByTestId('home-addons-off')
  await expect(note).toContainText('Add-ons are off on this server')
  await expect(note).toContainText('Studio is an add-on')
  // The Home blocks fade in; let them settle before the picture.
  await page.waitForTimeout(1000)
  await page.screenshot({ path: 'test-results/wiring-addons-off.png' })
  await note.getByRole('button', { name: 'Dismiss' }).click()
  await expect(note).toHaveCount(0)

  await page.getByRole('combobox', { name: 'Message' }).fill('/studio')
  await expect(page.getByRole('option', { name: /It is an add-on/ })).toBeVisible()
  await page.keyboard.press('Enter')
  await expect(page.getByTestId('home-addons-off')).toContainText('Studio is an add-on')
})
