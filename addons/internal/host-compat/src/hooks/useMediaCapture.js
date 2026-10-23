import { getSdk } from '../runtime.js'

// The host's useMediaCapture, through the SDK.
export function useMediaCapture(...args) {
  return getSdk().hooks.useMediaCapture(...args)
}

export function audioBufferToWavBlob(...args) {
  return getSdk().hooks.audioBufferToWavBlob(...args)
}
