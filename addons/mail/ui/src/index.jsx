// The entry of the Mail add-on. Its page edits the bindings that hold a
// mailbox connection through the core API, with the signed-in person's own
// session. It uses the host's React, router and i18n through the stand-ins of
// addons/internal/host-compat, like the GitHub add-on.
import { requireHost, setSdk } from '../../../internal/host-compat/src/runtime.js'

const NEEDS = ['ui.ConfirmDialog']

export async function createAddon(sdk) {
  requireHost(sdk, NEEDS)
  setSdk(sdk)
  const { createMail } = await import('./addon.jsx')
  return createMail(sdk)
}
