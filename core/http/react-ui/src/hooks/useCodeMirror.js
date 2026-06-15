import { useRef, useEffect } from 'react'
import { EditorView } from '@codemirror/view'
import { EditorState, Compartment } from '@codemirror/state'

export function useCodeMirror({ containerRef, value, onChange, extensions = [], dynamicExtensions = {} }) {
  const viewRef = useRef(null)
  const onChangeRef = useRef(onChange)
  const isExternalUpdate = useRef(false)
  const compartmentsRef = useRef({})

  onChangeRef.current = onChange

  // Create editor on mount (only depends on container and static extensions)
  useEffect(() => {
    if (!containerRef.current) return

    const listener = EditorView.updateListener.of(update => {
      if (update.docChanged && !isExternalUpdate.current) {
        onChangeRef.current(update.state.doc.toString())
      }
    })

    // Create compartments for each dynamic extension key
    const compartments = {}
    const compartmentExts = []
    for (const [key, ext] of Object.entries(dynamicExtensions)) {
      compartments[key] = new Compartment()
      compartmentExts.push(compartments[key].of(ext))
    }
    compartmentsRef.current = compartments

    const state = EditorState.create({
      doc: value,
      extensions: [...extensions, ...compartmentExts, listener],
    })

    const view = new EditorView({ state, parent: containerRef.current })
    viewRef.current = view

    return () => {
      view.destroy()
      viewRef.current = null
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [containerRef, extensions])

  // Reconfigure dynamic extensions without recreating the editor
  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const effects = []
    for (const [key, ext] of Object.entries(dynamicExtensions)) {
      const compartment = compartmentsRef.current[key]
      if (compartment) {
        effects.push(compartment.reconfigure(ext))
      }
    }
    if (effects.length > 0) {
      view.dispatch({ effects })
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [dynamicExtensions])

  // Sync external value changes into CM6
  useEffect(() => {
    const view = viewRef.current
    if (!view) return
    const current = view.state.doc.toString()
    if (value !== current) {
      isExternalUpdate.current = true
      view.dispatch({
        changes: { from: 0, to: current.length, insert: value },
      })
      isExternalUpdate.current = false
    }
  }, [value])

  return { view: viewRef }
}
