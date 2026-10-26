import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// The chat skills page and the switches panel of a chat, with the API stubbed.

const SKILLS = [
  { name: 'review', description: 'Check a change before it ships', layer: 'personal', path: '/workspace/home/.skills/review/SKILL.md', enabled: true, editable: true },
  { name: 'notes', description: 'Keep notes of a meeting', layer: 'folder', path: '/workspace/home/Atlas/.skills/notes/SKILL.md', enabled: true, editable: false },
  { name: 'team', description: 'How the team writes', layer: 'shared', path: '/workspace/shared-skills/team/SKILL.md', enabled: true, editable: false },
  { name: 'review', description: 'The shared review', layer: 'shared', path: '/workspace/shared-skills/review/SKILL.md', enabled: true, shadowed: 'personal', editable: false },
]

function skillsApi(page, state = {}) {
  const s = { skills: structuredClone(SKILLS), calls: [], detail: { body: 'Read the diff.\n' }, ...state }
  const record = (m, url, body) => s.calls.push({ method: m, url: new URL(url).pathname + new URL(url).search, body })
  return {
    s,
    async install() {
      await page.route(/\/api\/chats\/skills\/(personal|shared|builtin)\/[^/?]+(\?.*)?$/, async route => {
        const req = route.request()
        const name = new URL(req.url()).pathname.split('/').pop()
        if (req.method() === 'GET') {
          const sk = s.skills.find(k => k.name === name && !k.shadowed)
          return route.fulfill({ json: { name, layer: sk?.layer || 'personal', description: sk?.description || '', body: s.detail.body, editable: Boolean(sk?.editable) } })
        }
        record(req.method(), req.url(), req.postDataJSON?.())
        if (req.method() === 'DELETE') s.skills = s.skills.filter(k => k.name !== name)
        return route.fulfill({ json: { deleted: true, name, layer: 'personal', description: '', body: '' } })
      })
      await page.route(/\/api\/chats\/skills(\?.*)?$/, async route => {
        const req = route.request()
        if (req.method() === 'POST') {
          const b = req.postDataJSON()
          record('POST', req.url(), b)
          s.skills.push({ name: b.name, description: b.description, layer: b.layer, path: `/workspace/home/.skills/${b.name}/SKILL.md`, enabled: true, editable: true })
          return route.fulfill({ json: { ...b } })
        }
        return route.fulfill({ json: { skills: s.skills, notes: s.notes || [] } })
      })
    },
  }
}

test.describe('Chat skills page', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page) })

  test('lists skills by layer, and says which one another overrides', async ({ page }) => {
    await skillsApi(page).install()
    await page.goto('/app/chats/skills')
    await expect(page.getByRole('heading', { name: 'Personal' })).toBeVisible()
    await expect(page.getByTestId('skill-row').filter({ hasText: 'Check a change before it ships' })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Shared' })).toBeVisible()
    const shadowed = page.getByTestId('skill-row').filter({ hasText: 'The shared review' })
    await expect(shadowed).toContainText('overridden by personal')
    await expect(page.getByRole('heading', { name: 'In your folders' })).toBeVisible()
  })

  test('creates a personal skill', async ({ page }) => {
    const api = skillsApi(page)
    await api.install()
    await page.goto('/app/chats/skills')
    await page.getByRole('button', { name: 'New skill' }).click()
    await page.getByRole('textbox', { name: 'Name' }).fill('standup')
    await page.getByRole('textbox', { name: 'Description' }).fill('Write the standup note')
    await page.getByRole('textbox', { name: 'Instructions' }).fill('List what changed.')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect.poll(() => api.s.calls.find(c => c.method === 'POST')?.body).toMatchObject({ layer: 'personal', name: 'standup', description: 'Write the standup note', body: 'List what changed.' })
    await expect(page.getByTestId('skill-row').filter({ hasText: 'Write the standup note' })).toBeVisible()
  })

  test('edits a personal skill and deletes it', async ({ page }) => {
    const api = skillsApi(page)
    await api.install()
    await page.goto('/app/chats/skills')
    await page.getByTestId('skill-row').filter({ hasText: 'Check a change before it ships' }).getByRole('button', { name: 'Edit' }).click()
    await expect(page.getByRole('textbox', { name: 'Instructions' })).toHaveValue('Read the diff.\n')
    await page.getByRole('textbox', { name: 'Description' }).fill('Check carefully')
    await page.getByRole('button', { name: 'Save' }).click()
    await expect.poll(() => api.s.calls.find(c => c.method === 'PUT')?.body).toMatchObject({ description: 'Check carefully' })

    await page.getByTestId('skill-row').filter({ hasText: 'Keep notes' }).waitFor()
    await page.getByTestId('skill-row').filter({ hasText: 'Check' }).first().getByRole('button', { name: 'Delete' }).click()
    await expect.poll(() => api.s.calls.some(c => c.method === 'DELETE' && c.url.includes('/personal/review'))).toBe(true)
  })

  test('offers no edit on a shared skill to someone who may not change it', async ({ page }) => {
    await skillsApi(page).install()
    await page.goto('/app/chats/skills')
    const team = page.getByTestId('skill-row').filter({ hasText: 'How the team writes' })
    await expect(team).toBeVisible()
    await expect(team.getByRole('button', { name: 'Edit' })).toHaveCount(0)
    await expect(team.getByRole('button', { name: 'Delete' })).toHaveCount(0)
  })

  test('says why files are off', async ({ page }) => {
    await page.route(/\/api\/chats\/skills(\?.*)?$/, route => route.fulfill({
      status: 409, json: { error: { type: 'files_off', message: 'There is no data path, so files are off.', code: 409 } },
    }))
    await page.goto('/app/chats/skills')
    await expect(page.getByTestId('files-off')).toContainText('no data path')
  })
})
