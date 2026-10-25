// Where the Studio workspaces keep their history, as plain functions with no
// React in them so they can be tested on their own.

export const MEDIA_STORAGE_KEYS = {
  image: 'localai_image_history',
  video: 'localai_video_history',
  tts: 'localai_tts_history',
  sound: 'localai_sound_history',
  'audio-transform': 'localai_audio_transform_history',
  // Diarization kept no history before the Studio front page; it records the
  // file name, the model and a speaker count here, never the recording.
  diarization: 'localai_diarization_history',
}

export const FAVOURITES_KEY = 'localai_studio_favourites'

// Forget everything the Studio front page shows: every media history list and
// the favourites. 3D lives in IndexedDB, so the caller clears that with
// use3DHistory().clearAll(). Returns how many stored lists were removed.
export function clearAllMediaHistory() {
  let removed = 0
  for (const key of [...Object.values(MEDIA_STORAGE_KEYS), FAVOURITES_KEY]) {
    try {
      if (localStorage.getItem(key) !== null) removed += 1
      localStorage.removeItem(key)
    } catch { /* storage unavailable: nothing to clear */ }
  }
  return removed
}
