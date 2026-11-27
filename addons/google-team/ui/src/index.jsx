import { requireHost, setSdk } from '../../../internal/host-compat/src/runtime.js'

export async function createAddon(sdk) {
  requireHost(sdk, ['ui.ConfirmDialog'])
  setSdk(sdk)
  const { createTeam } = await import('./page.jsx')
  return createTeam()
}
