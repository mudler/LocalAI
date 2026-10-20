import { getSdk } from '../runtime.js'

// The host's useOperations, through the SDK.
export function useOperations(...args) {
  return getSdk().hooks.useOperations(...args)
}
