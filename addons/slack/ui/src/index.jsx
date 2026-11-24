// The entry of the Slack add-on. Its page edits the bindings of Slack apps
// through the core API, with the signed-in person's own session. It uses the
// host's React, router and i18n through the stand-ins of
// addons/internal/host-compat, like the Telegram add-on.
import { requireHost, setSdk } from '../../../internal/host-compat/src/runtime.js'

const NEEDS = ['ui.ConfirmDialog']

export async function createAddon(sdk) {
  requireHost(sdk, NEEDS)
  setSdk(sdk)
  const { createSlack } = await import('./addon.jsx')
  return createSlack(sdk)
}
