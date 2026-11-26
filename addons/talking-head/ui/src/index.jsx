// The entry of the Talking Head add-on. The page is written against the host
// modules, like the Talk page, and the stand-ins in addons/internal/host-compat
// answer those through the SDK.
import { requireHost, setSdk } from '../../../internal/host-compat/src/runtime.js'

const NEEDS = ['hooks.useModels', 'navigation.openApp', 'portal']

export async function createAddon(sdk) {
  requireHost(sdk, NEEDS.filter((n) => n.includes('.')))
  if (typeof sdk.portal !== 'function') throw new Error('This add-on needs a newer LocalAI: the host has no portal')
  setSdk(sdk)
  const { createTalkingHead } = await import('./addon.jsx')
  return createTalkingHead(sdk)
}
