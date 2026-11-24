// The entry of the PiKVM add-on. Its page edits the connections (a user and
// password, and an address) through the core API with the signed-in person's
// own session, and the per-device switches through the add-on's page tools. It
// uses the host's React, router and i18n through the stand-ins of
// addons/internal/host-compat, like the GitHub add-on.
import { requireHost, setSdk } from '../../../internal/host-compat/src/runtime.js'

const NEEDS = ['ui.ConfirmDialog']

export async function createAddon(sdk) {
  requireHost(sdk, NEEDS)
  setSdk(sdk)
  const { createPikvm } = await import('./addon.jsx')
  return createPikvm(sdk)
}
