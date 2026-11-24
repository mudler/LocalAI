// The models Live needs: one for speech to text and one for the voice. The
// pick is the first installed model of each capability, the same rule as the
// dictation button.

/**
 * @param {{ stt?: {id: string}[], tts?: {id: string}[] } | undefined} models
 * @returns {{ stt: string, tts: string }} an empty string when none is installed
 */
export function pickLiveModels(models) {
  return {
    stt: models?.stt?.[0]?.id || '',
    tts: models?.tts?.[0]?.id || '',
  }
}

/**
 * Gallery ids the setup sheet offers to install, best first. Each must exist
 * in gallery/index.yaml; a unit test reads the gallery and checks it.
 */
export const RECOMMENDED = {
  stt: ['whisper-base', 'parakeet-cpp-tdt_ctc-110m'],
  tts: ['kokoro-multi-lang-v1.0-sherpa'],
}
