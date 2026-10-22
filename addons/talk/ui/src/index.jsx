// The entry of the Talk add-on. The page is the one that used to be a core
// route; it keeps the host modules it was written against and the stand-ins in
// addons/internal/host-compat answer those through the SDK (see the Studio
// add-on's entry).
import { requireHost, setSdk } from '../../../internal/host-compat/src/runtime.js'

const NEEDS = [
  'ui.HomeModelPicker', 'ui.PageHeader', 'hooks.useMCPClient', 'navigation.openApp', 'portal',
]

export async function createAddon(sdk) {
  requireHost(sdk, NEEDS.filter((n) => n.includes('.')))
  if (typeof sdk.portal !== 'function') throw new Error('This add-on needs a newer LocalAI: the host has no portal')
  setSdk(sdk)
  const { createTalk } = await import('./addon.jsx')
  return createTalk(sdk)
}
