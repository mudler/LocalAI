import { getSdk } from '../runtime.js'

// The host's useObjectUrl, through the SDK.
export default function useObjectUrl(source) {
  return getSdk().hooks.useObjectUrl(source)
}
