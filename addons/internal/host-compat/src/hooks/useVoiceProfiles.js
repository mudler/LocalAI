import { getSdk } from '../runtime.js'

// The host's useVoiceProfiles, through the SDK.
export function useVoiceProfiles(...args) {
  return getSdk().hooks.useVoiceProfiles(...args)
}
