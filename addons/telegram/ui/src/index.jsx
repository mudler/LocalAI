// The entry of the Telegram add-on. Its page edits the bindings of Telegram bots
// through the core API, with the signed-in person's own session. It uses the
// host's React, router and i18n through the stand-ins of
// addons/internal/host-compat, like the Studio and Talk add-ons.
import { requireHost, setSdk } from '../../../internal/host-compat/src/runtime.js'

const NEEDS = ['ui.ConfirmDialog']

export async function createAddon(sdk) {
  requireHost(sdk, NEEDS)
  setSdk(sdk)
  const { createTelegram } = await import('./addon.jsx')
  return createTelegram(sdk)
}
