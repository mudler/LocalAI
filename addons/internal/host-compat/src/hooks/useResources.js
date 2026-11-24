import { getSdk } from '../runtime.js'

// The host's useResources, through the SDK.
export function useResources(...args) {
  return getSdk().hooks.useResources(...args)
}
