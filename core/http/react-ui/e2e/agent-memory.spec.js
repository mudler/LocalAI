// SPDX-License-Identifier: MIT
// These browser tests mock agent APIs, not the real-backend memory acceptance gate.
import { test, expect } from './coverage-fixtures.js'
import { installAgents, AGENTS } from './agents-fixtures.js'

const permissions = { version: 1, grants: [{ collection: 'handbook', search: false, delete_entries: false }] }
const memory = { version: 1, destination: 'handbook', add_memory: false, tool_storage: 'summary', automatic_ingestion: 'incoming_only' }
const open = (page, section) => page.locator(`[data-section="${section}"] .ag-fold__button`).click()

for (const theme of ['light', 'dark']) {
  test(`memory configuration defaults, independent grants and destination validation (${theme})`, async ({ page }) => {
    await page.addInitScript(value => localStorage.setItem('localai-theme', value), theme)
    const state = await installAgents(page)
    await page.goto('/app/agents/new')
    await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
    await page.getByLabel('Name', { exact: false }).first().fill('memory-agent')
    await open(page, 'MemorySettings')
    await expect(page.getByLabel('Enable add_memory tool')).not.toBeChecked()
    await expect(page.getByLabel('Automatic ingestion', { exact: true })).toHaveValue('off')
    await expect(page.getByLabel('Memory destination')).toHaveValue('')
    await page.getByLabel('Enable add_memory tool').check()
    await expect(page.getByTestId('agent-memory-settings').getByRole('alert')).toContainText('Choose an associated collection')
    await expect(page.getByLabel('Memory destination')).toHaveAttribute('aria-invalid', 'true')
    await page.getByRole('button', { name: 'Create agent', exact: true }).first().click()
    expect(state.calls.filter(c => c.method === 'POST' && c.url === '/api/chats/agents')).toHaveLength(0)
    await open(page, 'Collections')
    await page.getByLabel('Collections +').fill('handbook')
    await page.getByLabel('Collections +').press('Enter')
    const row = page.getByRole('group', { name: 'handbook', exact: true })
    await expect(row.getByLabel('Search', { exact: true })).toBeChecked()
    await expect(row.getByLabel('Delete entries')).not.toBeChecked()
    await row.getByLabel('Delete entries').check()
    await expect(row).toContainText('created by anyone')
    await row.getByLabel('Search', { exact: true }).uncheck()
    await row.getByLabel('Delete entries').uncheck()
    await page.getByLabel('Memory destination').selectOption('handbook')
    await expect(page.getByTestId('agent-memory-settings').getByRole('alert')).toHaveCount(0)
    await expect(page.getByTestId('agent-memory-settings')).toContainText('explicit, successful add_memory calls')
    await page.getByLabel('Tool storage', { exact: true }).selectOption('summary')
    await page.getByLabel('Enable add_memory tool').uncheck()
    await page.getByLabel('Automatic ingestion', { exact: true }).selectOption('incoming_only')
    await page.getByRole('button', { name: 'Create agent', exact: true }).first().click()
    await expect.poll(() => state.agents.length).toBe(3)
    expect(state.agents[2]).toMatchObject({ collections: [], collection_permissions: permissions, memory })
  })
}

test('editing preserves all-disabled destination and blocks removal without retargeting', async ({ page }) => {
  const state = await installAgents(page, { agents: [{ ...AGENTS[0], collections: [], collection_permissions: permissions, memory }] })
  await page.goto('/app/agents/a1/edit')
  await open(page, 'MemorySettings')
  await expect(page.getByLabel('Memory destination')).toHaveValue('handbook')
  await expect(page.getByLabel('Automatic ingestion', { exact: true })).toHaveValue('incoming_only')
  await open(page, 'Collections')
  await page.getByRole('button', { name: 'handbook ×', exact: true }).click()
  await expect(page.getByTestId('agent-memory-settings').getByRole('alert')).toContainText('no longer associated')
  await expect(page.getByLabel('Memory destination')).toHaveValue('handbook')
  await page.getByRole('button', { name: 'Save changes', exact: true }).first().click()
  expect(state.calls.filter(c => c.method === 'PUT')).toHaveLength(0)
  await page.getByLabel('Collections +').fill('handbook')
  await page.getByLabel('Collections +').press('Enter')
  await page.getByRole('group', { name: 'handbook', exact: true }).getByLabel('Search', { exact: true }).uncheck()
  await page.getByRole('button', { name: 'Save changes', exact: true }).first().click()
  await expect(page).toHaveURL(/\/app\/agents$/)
  expect(state.agents[0]).toMatchObject({ collections: [], collection_permissions: permissions, memory })
  await page.goto('/app/agents/a1')
  await expect(page.getByTestId('agent-memory-facts')).toContainText('Reader: None')
  await expect(page.getByTestId('agent-memory-facts')).toContainText('Recorder: handbook')
  await expect(page.getByTestId('agent-memory-facts')).toContainText('Incoming messages only')
})

test('versioned agent file round trip keeps an all-false destination', async ({ page }) => {
  const state = await installAgents(page, { agents: [{ ...AGENTS[0], collections: [], collection_permissions: permissions, memory }] })
  await page.goto('/app/agents')
  await page.locator('[data-agent="research-assistant"]').getByRole('button', { name: /More actions/ }).click()
  const downloading = page.waitForEvent('download')
  await page.getByRole('menuitem', { name: 'Export' }).click()
  const file = await downloading
  await page.locator('input[type=file]').first().setInputFiles(await file.path())
  await expect(page).toHaveURL(/\/app\/agents\/a2$/)
  expect(state.agents[1]).toMatchObject({ collections: [], collection_permissions: permissions, memory })
  await expect(page.getByTestId('agent-memory-facts')).toContainText('Recorder: handbook')
})
