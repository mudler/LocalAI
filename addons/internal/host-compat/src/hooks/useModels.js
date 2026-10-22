import { getSdk } from '../runtime.js'

// The host's useModels, through the SDK.
export function useModels(...args) {
  return getSdk().hooks.useModels(...args)
}
