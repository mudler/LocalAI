import { useState, useCallback, useRef, useEffect, useMemo } from 'react'
import { generateId } from '../utils/format'

const STORAGE_KEYS = {
  image: 'localai_image_history',
  video: 'localai_video_history',
  tts: 'localai_tts_history',
  sound: 'localai_sound_history',
}

const SAVE_DEBOUNCE_MS = 500
const MAX_ENTRIES = 100

function loadEntries(key) {
  try {
    const stored = localStorage.getItem(key)
    if (stored) {
      const data = JSON.parse(stored)
      if (Array.isArray(data)) return data
    }
  } catch (_e) {
    localStorage.removeItem(key)
  }
  return []
}

function saveEntries(key, entries) {
  try {
    localStorage.setItem(key, JSON.stringify(entries))
  } catch (err) {
    if (err.name === 'QuotaExceededError' || err.code === 22) {
      console.warn('localStorage quota exceeded for media history')
    }
  }
}

export function useMediaHistory(mediaType) {
  const storageKey = STORAGE_KEYS[mediaType]
  const [entries, setEntries] = useState(() => loadEntries(storageKey))
  const [selectedId, setSelectedId] = useState(null)
  const saveTimer = useRef(null)
  const dirty = useRef(false)
  const entriesRef = useRef(entries)
  entriesRef.current = entries

  // Debounced save — only when dirty
  useEffect(() => {
    if (!dirty.current) return
    clearTimeout(saveTimer.current)
    saveTimer.current = setTimeout(() => {
      saveEntries(storageKey, entries)
      dirty.current = false
    }, SAVE_DEBOUNCE_MS)
    return () => clearTimeout(saveTimer.current)
  }, [entries, storageKey])

  // Flush pending save on unmount
  useEffect(() => {
    return () => {
      clearTimeout(saveTimer.current)
      if (dirty.current) {
        saveEntries(storageKey, entriesRef.current)
      }
    }
  }, [storageKey])

  const addEntry = useCallback(({ prompt, model, params, results }) => {
    const entry = {
      id: generateId(),
      prompt,
      model,
      params: params || {},
      results: results || [],
      createdAt: Date.now(),
    }
    dirty.current = true
    setEntries(prev => {
      const updated = [entry, ...prev]
      if (updated.length > MAX_ENTRIES) updated.length = MAX_ENTRIES
      return updated
    })
  }, [])

  const deleteEntry = useCallback((id) => {
    dirty.current = true
    setEntries(prev => prev.filter(e => e.id !== id))
    setSelectedId(prev => prev === id ? null : prev)
  }, [])

  const clearAll = useCallback(() => {
    dirty.current = true
    setEntries([])
    setSelectedId(null)
  }, [])

  const selectEntry = useCallback((id) => {
    setSelectedId(prev => prev === id ? null : id)
  }, [])

  const selectedEntry = useMemo(
    () => entries.find(e => e.id === selectedId) || null,
    [entries, selectedId]
  )

  const historyProps = useMemo(() => ({
    entries,
    selectedId,
    onSelect: selectEntry,
    onDelete: deleteEntry,
    onClearAll: clearAll,
    mediaType,
  }), [entries, selectedId, selectEntry, deleteEntry, clearAll, mediaType])

  return { addEntry, selectEntry, selectedEntry, historyProps }
}
