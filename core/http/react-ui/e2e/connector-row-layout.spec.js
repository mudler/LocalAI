import { test, expect } from './coverage-fixtures.js'
import { mockChat } from './chat-fixtures.js'
import { installStudioAddons } from './studio-addon.js'
import { connectorApi } from './connector-fixtures.js'

// The saved-connection rows of the messaging add-ons are drawn with the main
// app's style sheet loaded too, which also defines .cn-row for the connections
// list. The add-on rows use their own class names, and the row text must keep
// its width whichever sheet loads last.

const SHOTS = process.env.UI_SHOTS_DIR
const KINDS = [
  ['slack', '/app/addons/slack/apps'],
  ['telegram', '/app/addons/telegram/bots'],
  ['discord', '/app/addons/discord/bots'],
]

for (const [kind, page_] of KINDS) {
  for (const width of [1280, 375]) {
    test(`${kind}: the text of a saved row is not squeezed into a narrow column at ${width} px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 })
      await mockChat(page)
      await installStudioAddons(page, { ids: [kind] })
      const item = { id: 'b1', name: 'Team app', kind, agent_id: 'a1', enabled: true, accept_any: false, allow: ['U001', 'U002', 'U003', 'U004'], has_token: true, caps: [], grants: [], channels: [], paired: [], reply_mode: 'stream', conversation: 'sender' }
      await connectorApi(page, [item], kind).install()
      // In the app the main sheet loads when its page is first opened, which can
      // be after the add-on's: take its text from that page, then add it after
      // the add-on sheet here.
      await page.goto('/app/chats/connectors')
      const href = await page.waitForFunction(() => [...document.styleSheets].find((s) => { try { return [...s.cssRules].some((r) => r.selectorText === '.cn-row') } catch { return false } })?.href)
      const mainCss = await page.evaluate((u) => fetch(u).then((r) => r.text()), await href.jsonValue())
      await page.goto(page_)
      await expect(page.getByTestId('connector-row')).toBeVisible()
      await page.evaluate((css) => { const el = document.createElement('style'); el.textContent = css; document.head.appendChild(el) }, mainCss)
      const text = page.getByTestId('connector-row').locator('.cn-conn__text')
      await expect(text).toBeVisible()
      const box = await text.boundingBox()
      expect(box.width, 'row text width').toBeGreaterThan(120)
      if (SHOTS) await page.getByTestId('connector-row').screenshot({ path: `${SHOTS}/row-${kind}-${width}.png` })
    })
  }
}
