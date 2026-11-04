import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi, ROLES_KIND } from './connector-fixtures.js'

// The pages of the add-ons follow the language of the web UI. The words of the
// shared connector page and of each add-on page are bundles of the add-on, in
// every language of the web UI; a key a bundle lacks reads in English. These
// specs set the language to Italian, draw the real built bundles with the core
// API stubbed, and check a few translated words. With UI_SHOTS_DIR set, they
// also save screenshots (light and dark, wide and phone) for a visual review.

const SHOTS = process.env.UI_SHOTS_DIR
const SLACK = '/app/addons/slack/apps'
const SAVED = {
  id: 'b1', name: 'Team app', kind: 'slack', agent_id: 'a1', enabled: true, accept_any: false, allow: ['U001'], has_token: true,
  caps: ['memory.write', 'files.upload', 'triggers.propose', 'talkback.channels'],
  grants: [{ role: 'workspace_admin', caps: ['memory.write'] }], channels: [{ id: 'C0123', label: 'ops', approved: true }],
  paired: [{ sender_id: 'U001', paired_at: '2026-11-01T10:00:00Z', expires_at: '2027-01-30T10:00:00Z' }],
  approval: { mode: 'ask', acts: ['act.files'] },
}
const VIEWS = [
  { name: 'wide', width: 1280, height: 900 },
  { name: 'phone', width: 390, height: 844 },
]

async function setLanguage(page, lng) {
  await page.addInitScript((l) => localStorage.setItem('localai-language', l), lng)
}

// The elements inside the box that run past its right edge or clip their own
// text. A long Italian word that does not fit shows up here.
function overflowing(box) {
  return box.evaluate((root) => {
    const edge = root.getBoundingClientRect().right
    const out = []
    for (const el of root.querySelectorAll('*')) {
      const r = el.getBoundingClientRect()
      if (r.width <= 1 || r.height <= 1 || el.closest('.cn-vh')) continue
      const style = getComputedStyle(el)
      const scrolls = ['auto', 'scroll'].includes(style.overflowX)
      const clipped = !scrolls && el.scrollWidth > el.clientWidth + 1 && style.display !== 'inline' && !['INPUT', 'SELECT', 'TEXTAREA', 'svg'].includes(el.tagName)
      if (r.right > edge + 1 || clipped) out.push(`${el.tagName.toLowerCase()}.${String(el.className).split(' ')[0]} "${(el.textContent || '').trim().slice(0, 40)}"`)
    }
    return out
  })
}

// Saves a screenshot of the element in each theme and size, and fails when
// something in it runs sideways out of its box.
async function shots(page, name, locator) {
  for (const theme of ['light', 'dark']) {
    await page.emulateMedia({ colorScheme: theme })
    await page.evaluate((t) => { document.documentElement.setAttribute('data-theme', t) }, theme)
    for (const v of VIEWS) {
      await page.setViewportSize({ width: v.width, height: v.height })
      await locator.scrollIntoViewIfNeeded()
      expect(await overflowing(locator), `${name} ${theme} ${v.name}: text runs out of its box`).toEqual([])
      if (SHOTS) await locator.screenshot({ path: `${SHOTS}/${name}-${theme}-${v.name}.png` })
    }
  }
}

test.describe('Add-on pages in Italian', () => {
  test.beforeEach(async ({ page }) => {
    await setLanguage(page, 'it')
    await mockChat(page)
    await installStudioAddons(page, { ids: ['slack'] })
  })

  test('the connection page draws the shared words in Italian, with a name that stays in English', async ({ page }) => {
    const a = connectorApi(page, [SAVED], 'slack', [], undefined, { kindExtra: ROLES_KIND })
    await a.install()
    await page.goto(SLACK)
    // The words of the add-on itself.
    await expect(page.getByRole('button', { name: 'Nuova app Slack' })).toBeVisible()
    await page.getByRole('button', { name: 'Modifica', exact: true }).click()
    await expect(page.getByLabel('Token del bot')).toBeVisible()
    // The shared words: readiness, what the bot can do, permissions, approval, pairing.
    await expect(page.getByTestId('connector-readiness')).toContainText('Stato di preparazione')
    await page.getByTestId('connector-ready-toggle').click()
    await expect(page.getByTestId('connector-ready-toggle')).toContainText('Mostra solo ciò che richiede attenzione')
    await expect(page.getByTestId('connector-ready-who')).toContainText('Chi può usarlo')
    await expect(page.getByTestId('connector-ready-who')).toContainText('1 persona o chat è consentita.')
    await expect(page.getByTestId('connector-can')).toContainText('Cosa può fare questo bot')
    await expect(page.getByTestId('connector-permissions')).toContainText('Cosa possono fare i mittenti')
    await expect(page.getByTestId('connector-pairing')).toContainText('Mittenti abbinati')
    await expect(page.getByTestId('connector-pair-start')).toHaveText('Abbina un mittente')
    await expect(page.getByTestId('connector-approval')).toContainText('Le esecuzioni possono agire')
    // No English left in the words of the form that the bundles own.
    await expect(page.getByTestId('connector-form')).not.toContainText('What senders may do')
    await expect(page.getByTestId('connector-form')).not.toContainText('Paired senders')
    await expect(page.getByTestId('connector-form')).not.toContainText('Readiness')
  })

  test('one person is singular and two are plural', async ({ page }) => {
    const a = connectorApi(page, [{ ...SAVED, allow: ['U001', 'U002'] }], 'slack', [], undefined, { kindExtra: ROLES_KIND })
    await a.install()
    await page.goto(SLACK)
    await page.getByRole('button', { name: 'Modifica', exact: true }).click()
    await page.getByTestId('connector-ready-toggle').click()
    await expect(page.getByTestId('connector-ready-who')).toContainText('2 persone o chat sono consentite.')
  })

  test('screenshots of the connection page', async ({ page }) => {
    const a = connectorApi(page, [SAVED], 'slack', [], undefined, { kindExtra: ROLES_KIND })
    await a.install()
    await page.goto(SLACK)
    await page.getByRole('button', { name: 'Modifica', exact: true }).click()
    await page.getByTestId('connector-ready-toggle').click()
    await expect(page.getByTestId('connector-ready-who')).toBeVisible()
    await expect(page.getByTestId('connector-can-memory.write')).toBeVisible()
    await shots(page, 'it-connection', page.getByTestId('connector-form'))
    await shots(page, 'it-readiness', page.getByTestId('connector-readiness'))
    await shots(page, 'it-can', page.getByTestId('connector-can'))
    await shots(page, 'it-permissions', page.getByTestId('connector-permissions'))
    await shots(page, 'it-approval', page.getByTestId('connector-approval'))
    await shots(page, 'it-pairing', page.getByTestId('connector-pairing'))
  })

  test('a new connection shows the states that need attention, in Italian', async ({ page }) => {
    await connectorApi(page, [], 'slack', [], undefined, { kindExtra: ROLES_KIND }).install()
    await page.goto(SLACK)
    await page.getByRole('button', { name: 'Nuova app Slack' }).click()
    await expect(page.getByTestId('connector-ready-token')).toContainText('Richiede attenzione')
    await expect(page.getByTestId('connector-ready-agent')).toContainText('Scegli l\'agente che risponde.')
    await shots(page, 'it-readiness-new', page.getByTestId('connector-readiness'))
    await shots(page, 'it-form-new', page.getByTestId('connector-form'))
  })
})

test.describe('Add-on pages follow the language', () => {
  for (const lng of ['de', 'es', 'fr', 'id', 'ko', 'pt-BR', 'zh-CN']) {
    test(`the connection page in ${lng} has no text running out of its box on a phone`, async ({ page }) => {
      await setLanguage(page, lng)
      await mockChat(page)
      await installStudioAddons(page, { ids: ['slack'] })
      await connectorApi(page, [SAVED], 'slack', [], undefined, { kindExtra: ROLES_KIND }).install()
      await page.setViewportSize({ width: 390, height: 844 })
      await page.goto(SLACK)
      await page.locator('.cn-row__actions .dk-btn').nth(2).click()
      await page.getByTestId('connector-ready-toggle').click()
      await expect(page.getByTestId('connector-pairing')).toBeVisible()
      expect(await overflowing(page.getByTestId('connector-form')), lng).toEqual([])
    })
  }


  test('a language the bundles lack reads in English', async ({ page }) => {
    await setLanguage(page, 'sv')
    await mockChat(page)
    await installStudioAddons(page, { ids: ['slack'] })
    await connectorApi(page, [SAVED], 'slack', [], undefined, { kindExtra: ROLES_KIND }).install()
    await page.goto(SLACK)
    await expect(page.getByRole('button', { name: 'New Slack app' })).toBeVisible()
  })
})

// The Jellyfin and Home Assistant pages, with the core API and the add-on tools stubbed.
const ADDRESS_ONLY = { files: undefined, agent_optional: true }
const CREDENTIALS = (kind, resource) => ({ id: 'b1', name: kind === 'jellyfin' ? 'Salotto' : 'Casa', kind, agent_id: '', credentials_only: true, enabled: false, accept_any: false, allow: [], resources: [resource], write_mode: 'propose', has_token: true })
const POLICY = { allow: ['light.turn_on', 'light.turn_off', 'climate.set_temperature', 'script.turn_on'], excludeDomains: ['lock', 'camera'], excludeAreas: [], excludePatterns: [], excludeLabel: 'sensitive', maxTargets: 5, pins: [] }
const HARD = { domains: ['lock', 'alarm_control_panel', 'siren'], serviceNames: ['restart', 'stop'], servicePrefixes: ['reload'], coverClasses: ['blind'] }
const dispatch = (body) => {
  const a = body.arguments || {}
  const ok = (data) => ({ status: 200, contentType: 'application/json', body: JSON.stringify({ payload: { data } }) })
  if (a.tool === 'get-policy') return ok({ policy: POLICY, defaults: POLICY, admin: true, broken: false, hard: HARD, physical: ['climate', 'script'] })
  if (a.tool === 'get-config') return ok({ ok: true, version: '2026.11.0', name: 'Casa', integrations: 12 })
  return null
}

test.describe('Jellyfin and Home Assistant in Italian', () => {
  test.beforeEach(async ({ page }) => {
    await setLanguage(page, 'it')
    await mockChat(page)
  })

  test('the Jellyfin page', async ({ page }) => {
    await installStudioAddons(page, { ids: ['jellyfin'], dispatch })
    await connectorApi(page, [CREDENTIALS('jellyfin', 'http://192.168.1.10:8096')], 'jellyfin', [], undefined, { capabilities: [], kindExtra: { title: 'Jellyfin', ...ADDRESS_ONLY } }).install()
    await page.goto('/app/addons/jellyfin/jellyfin')
    await expect(page.getByRole('button', { name: 'Nuova connessione Jellyfin' })).toBeVisible()
    await expect(page.getByTestId('connector-row')).toContainText('Solo credenziali')
    await expect(page.locator('.cn-page')).toContainText('Come creare la chiave')
    await shots(page, 'it-jellyfin', page.locator('.cn-page'))
    await page.getByRole('button', { name: 'Modifica', exact: true }).click()
    await expect(page.getByLabel('Chiave API')).toBeVisible()
    await shots(page, 'it-jellyfin-form', page.getByTestId('connector-form'))
  })

  test('the Home Assistant page', async ({ page }) => {
    await installStudioAddons(page, { ids: ['home-assistant'], dispatch })
    await connectorApi(page, [CREDENTIALS('home-assistant', 'https://ha.example.org:8123')], 'home-assistant', [], undefined, { capabilities: [], kindExtra: { title: 'Home Assistant', ...ADDRESS_ONLY } }).install()
    await page.goto('/app/addons/home-assistant/apps')
    await expect(page.getByRole('button', { name: 'Nuova connessione Home Assistant' })).toBeVisible()
    await page.getByRole('button', { name: 'Prova' }).click()
    await expect(page.getByTestId('home-assistant-checks')).toContainText('Raggiungibile. Home Assistant 2026.11.0, 12 integrazioni.')
    await expect(page.getByTestId('home-assistant-policy')).toContainText('Cosa possono fare le chat nella tua casa')
    await shots(page, 'it-home-assistant', page.locator('.cn-page'))
    await page.getByRole('button', { name: 'Modifica', exact: true }).click()
    await shots(page, 'it-home-assistant-form', page.getByTestId('connector-form'))
  })
})
