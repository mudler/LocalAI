// The entry of the Google Drive add-on. Its page edits the folders and the
// write mode of the connections, through the core API with the signed-in
// person's own session; a person signs in to Google on the Connectors page of
// the host. It also draws the card of a change that waits for approval. It
// uses the host's React, router and i18n through the stand-ins of
// addons/internal/host-compat, like the GitHub add-on.
import { requireHost, setSdk } from '../../../internal/host-compat/src/runtime.js'

const NEEDS = ['ui.ConfirmDialog']

export async function createAddon(sdk) {
  requireHost(sdk, NEEDS)
  setSdk(sdk)
  const { createGdrive } = await import('./addon.jsx')
  return createGdrive(sdk)
}
