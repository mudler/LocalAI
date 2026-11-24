// The entry of the SSH add-on. Its page edits the connections (an address, a
// pinned host key and a private key or password) through the core API with the
// signed-in person's own session. It uses the host's React, router and i18n
// through the stand-ins of addons/internal/host-compat, like the PiKVM add-on.
import { requireHost, setSdk } from '../../../internal/host-compat/src/runtime.js'

const NEEDS = ['ui.ConfirmDialog']

export async function createAddon(sdk) {
  requireHost(sdk, NEEDS)
  setSdk(sdk)
  const { createSsh } = await import('./addon.jsx')
  return createSsh(sdk)
}
