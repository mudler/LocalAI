// The entry of the Discord add-on. Its page edits the bindings of Discord bots
// through the core API, with the signed-in person's own session. It uses the
// host's React, router and i18n through the stand-ins of
// addons/internal/host-compat, like the Telegram and Slack add-ons.
import { requireHost, setSdk } from '../../../internal/host-compat/src/runtime.js'

const NEEDS = ['ui.ConfirmDialog']

export async function createAddon(sdk) {
  requireHost(sdk, NEEDS)
  setSdk(sdk)
  const { createDiscord } = await import('./addon.jsx')
  return createDiscord(sdk)
}
