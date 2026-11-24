// The entry of the Studio add-on. The pages are the ones that used to be core
// routes; they keep the host modules they were written against (React, the
// router, i18next, the shared components), and the stand-ins in
// addons/internal/host-compat answer those through the SDK. The page code is
// loaded after the SDK is set, because a stand-in reads the SDK when it starts.
import { requireHost, setSdk } from '../../../internal/host-compat/src/runtime.js'

// What the pages need from the host beyond SDK 2's first set.
const NEEDS = [
  'ui.LoadingSpinner', 'ui.Modal', 'ui.Lightbox', 'ui.ModelSelector', 'ui.PageHeader', 'ui.ConfirmDialog',
  'ui.MediaInput', 'ui.WaveformPlayer', 'hooks.useModels', 'hooks.useResources', 'hooks.useVoiceProfiles',
  'hooks.useOperations', 'hooks.useMediaCapture', 'hooks.useObjectUrl', 'navigation.openApp',
]

export async function createAddon(sdk) {
  requireHost(sdk, NEEDS)
  setSdk(sdk)
  const { createStudio } = await import('./addon.jsx')
  return createStudio(sdk)
}
