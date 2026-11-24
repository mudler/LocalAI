import { getSdk } from '../runtime.js'

// The host's useMCPClient, through the SDK.
export function useMCPClient(...args) {
  return getSdk().hooks.useMCPClient(...args)
}
