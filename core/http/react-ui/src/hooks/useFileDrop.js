import { useEffect, useRef, useState } from 'react'
import { droppedFiles, hasFiles } from '../utils/dropFiles'

// Files dropped on the page go to onFiles, as the attach button stages them.
// The whole page is the target, so a drop does not have to hit the composer;
// the composer shows the drop state (dragging). Only drags that carry files
// count: selected text, a link or a row dragged inside the page do not.
//
// With enabled false a file drag shows nothing and a drop stages nothing, but
// the browser still does not leave the app to open the file. A drop that an
// element already took (it called preventDefault) is not staged twice.
export function useFileDrop({ enabled, onFiles }) {
  const [dragging, setDragging] = useState(false)
  const depth = useRef(0)
  const handler = useRef(onFiles)
  useEffect(() => { handler.current = onFiles })

  useEffect(() => {
    const reset = () => { depth.current = 0; setDragging(false) }
    const enter = (e) => {
      if (!hasFiles(e.dataTransfer)) return
      depth.current++
      if (enabled) setDragging(true)
    }
    const over = (e) => {
      if (!hasFiles(e.dataTransfer)) return
      e.preventDefault()
      if (e.dataTransfer) e.dataTransfer.dropEffect = enabled ? 'copy' : 'none'
    }
    const leave = (e) => {
      if (!hasFiles(e.dataTransfer)) return
      depth.current = Math.max(0, depth.current - 1)
      // Leaving the window has no element to enter.
      if (depth.current === 0 || e.relatedTarget === null) reset()
    }
    const drop = (e) => {
      if (!hasFiles(e.dataTransfer)) return
      const taken = e.defaultPrevented
      e.preventDefault()
      reset()
      if (!enabled || taken) return
      const list = droppedFiles(e.dataTransfer)
      if (list.length) handler.current(list)
    }
    window.addEventListener('dragenter', enter)
    window.addEventListener('dragover', over)
    window.addEventListener('dragleave', leave)
    window.addEventListener('drop', drop)
    window.addEventListener('dragend', reset)
    return () => {
      window.removeEventListener('dragenter', enter)
      window.removeEventListener('dragover', over)
      window.removeEventListener('dragleave', leave)
      window.removeEventListener('drop', drop)
      window.removeEventListener('dragend', reset)
      reset()
    }
  }, [enabled])

  return { dragging: enabled && dragging }
}
