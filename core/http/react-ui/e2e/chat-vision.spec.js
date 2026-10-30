import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'

// A cloud-proxy model sees images only when its config says so: LocalAI cannot
// ask the upstream. The model editor offers a switch for it, and the chat says
// at attach time when the chosen model cannot look at an image. The API is
// stubbed.

const PNG = Buffer.from('89504e470d0a1a0a0000000d4948445200000001000000010806000000', 'hex')

const PROXY_METADATA = {
  sections: [
    { id: 'general', label: 'General', icon: 'settings', order: 0 },
    { id: 'proxy', label: 'Cloud Proxy', icon: 'cloud', order: 50 },
  ],
  fields: [
    { path: 'name', yaml_key: 'name', go_type: 'string', ui_type: 'string', section: 'general', label: 'Model Name', component: 'input', order: 0 },
    { path: 'backend', yaml_key: 'backend', go_type: 'string', ui_type: 'string', section: 'general', label: 'Backend', component: 'input', order: 1 },
    { path: 'known_usecases', yaml_key: 'known_usecases', go_type: '[]string', ui_type: '[]string', section: 'general', label: 'Known Usecases', component: 'string-list', order: 2 },
    { path: 'proxy.upstream_url', yaml_key: 'upstream_url', go_type: 'string', ui_type: 'string', section: 'proxy', label: 'Proxy Upstream URL', component: 'input', order: 210 },
  ],
}

async function mockEditor(page, yaml) {
  await page.route('**/api/auth/status', route => route.fulfill({ json: { authEnabled: false, staticApiKeyRequired: false, providers: [] } }))
  await page.route('**/api/models/config-metadata*', route => route.fulfill({ json: PROXY_METADATA }))
  await page.route('**/api/models/config-metadata/autocomplete/**', route => route.fulfill({ json: { values: [] } }))
  await page.route('**/api/models/edit/**', route => route.fulfill({ json: { config: yaml, name: 'cloud' } }))
  const patches = []
  await page.route('**/api/models/config-json/**', route => {
    if (route.request().method() === 'PATCH') patches.push(route.request().postDataJSON())
    return route.fulfill({ json: {} })
  })
  return patches
}

test.describe('Cloud-proxy model: can look at images', () => {
  test('the switch writes vision into known_usecases', async ({ page }) => {
    const patches = await mockEditor(page, 'name: cloud\nbackend: cloud-proxy\nknown_usecases: [chat]\nproxy:\n  upstream_url: https://upstream.example/v1/chat/completions\n')
    await page.goto('/app/model-editor/cloud')
    const sw = page.getByTestId('proxy-vision-switch')
    await expect(sw).toContainText('This model can look at images')
    await expect(sw).toContainText('LocalAI cannot check it')
    const box = sw.locator('input[type=checkbox]')
    await expect(box).not.toBeChecked()

    await sw.locator('label.toggle').click()
    await expect(box).toBeChecked()
    await page.getByRole('button', { name: 'Save Changes' }).click()
    await expect.poll(() => patches.length).toBe(1)
    expect(patches[0].known_usecases).toEqual(['chat', 'vision'])
  })

  test('the switch shows a declared vision and turns it off', async ({ page }) => {
    const patches = await mockEditor(page, 'name: cloud\nbackend: cloud-proxy\nknown_usecases: [chat, vision]\nproxy:\n  upstream_url: https://upstream.example/v1/chat/completions\n')
    await page.goto('/app/model-editor/cloud')
    const sw = page.getByTestId('proxy-vision-switch')
    await expect(sw.locator('input[type=checkbox]')).toBeChecked()
    await sw.locator('label.toggle').click()
    await page.getByRole('button', { name: 'Save Changes' }).click()
    await expect.poll(() => patches.length).toBe(1)
    expect(patches[0].known_usecases).toEqual(['chat'])
  })

  test('a cloud model in translate mode has no switch: that mode sends text only', async ({ page }) => {
    await mockEditor(page, 'name: cloud\nbackend: cloud-proxy\nknown_usecases: [chat]\nproxy:\n  mode: translate\n  upstream_url: https://upstream.example/v1/messages\n')
    await page.goto('/app/model-editor/cloud')
    await expect(page.getByText('Proxy Upstream URL')).toBeVisible({ timeout: 10_000 })
    await expect(page.getByTestId('proxy-vision-switch')).toHaveCount(0)
  })

  test('a model that is not a cloud proxy has no switch', async ({ page }) => {
    await mockEditor(page, 'name: local\nbackend: llama-cpp\nknown_usecases: [chat]\nproxy:\n  upstream_url: https://upstream.example/v1/chat/completions\n')
    await page.goto('/app/model-editor/local')
    await expect(page.getByText('Proxy Upstream URL')).toBeVisible({ timeout: 10_000 })
    await expect(page.getByTestId('proxy-vision-switch')).toHaveCount(0)
  })
})

const MODELS = [
  { id: 'cloud', capabilities: ['chat'] },
  { id: 'cloud-vl', capabilities: ['chat', 'vision'] },
]

async function stubChat(page, model, imageModel) {
  const detail = {
    id: 'c1', title: 'Photo', model, kind: 'chat', created: '2026-10-25T10:00:00Z', updated: '2026-10-25T10:00:00Z',
    running: false, last_turn: 'done', pending: [],
  }
  await page.route('**/api/chats/c1/events', route => route.abort())
  await page.route(/\/api\/chats\/c1\/messages(\?.*)?$/, route => route.fulfill({ json: { messages: [] } }))
  await page.route('**/api/chats/c1', route => route.fulfill({ json: detail }))
  await page.route(/\/api\/chats(\?.*)?$/, route => route.fulfill({
    json: { conversations: [{ ...detail, pending: undefined }], persistent: true, files: true, image_model: imageModel },
  }))
}

const attachPhoto = page => page.getByTestId('chat-file-input').setInputFiles({ name: 'photo.png', mimeType: 'image/png', buffer: PNG })

test.describe('Chat: an image for a model that cannot look at it', () => {
  test.beforeEach(async ({ page }) => { await mockChat(page, { models: MODELS, loaded: [] }) })

  test('names the model that will describe the image', async ({ page }) => {
    await stubChat(page, 'cloud', 'helper-vl')
    await page.goto('/app/chats/c1')
    await expect(page.getByTestId('chat-input')).toBeVisible()
    await expect(page.getByTestId('chat-vision-notice')).toHaveCount(0)
    await attachPhoto(page)
    await expect(page.getByTestId('chat-vision-notice'))
      .toHaveText("This model can't look at images. Pick another model, or the image will be described by helper-vl.")
  })

  test('says when no model can describe the image', async ({ page }) => {
    await stubChat(page, 'cloud', undefined)
    await page.goto('/app/chats/c1')
    await attachPhoto(page)
    await expect(page.getByTestId('chat-vision-notice'))
      .toHaveText("This model can't look at images, and no model here can describe them. Pick a model that can look at images.")
  })

  test('says nothing for a model that can look at images', async ({ page }) => {
    await stubChat(page, 'cloud-vl', '')
    await page.goto('/app/chats/c1')
    await attachPhoto(page)
    await expect(page.getByTestId('attachment')).toHaveCount(1)
    await expect(page.getByTestId('chat-vision-notice')).toHaveCount(0)
  })
})
