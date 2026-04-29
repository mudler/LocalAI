import { useState, useEffect, useRef } from 'react'

export default function ChatHeaderOverflow({
  isAdmin,
  hasModel,
  manageMode,
  onToggleManage,
  onShowModelInfo,
  onEditModel,
  onExport,
  onClearHistory,
}) {
  const [open, setOpen] = useState(false)
  const ref = useRef(null)
  const triggerRef = useRef(null)

  useEffect(() => {
    if (!open) return
    const handleClick = (e) => {
      if (ref.current && !ref.current.contains(e.target)) setOpen(false)
    }
    const handleKey = (e) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        setOpen(false)
        triggerRef.current?.focus()
      }
    }
    document.addEventListener('mousedown', handleClick)
    document.addEventListener('keydown', handleKey)
    return () => {
      document.removeEventListener('mousedown', handleClick)
      document.removeEventListener('keydown', handleKey)
    }
  }, [open])

  const close = () => setOpen(false)

  return (
    <div className="chat-overflow-menu" ref={ref}>
      <button
        ref={triggerRef}
        type="button"
        className={`btn btn-secondary btn-sm chat-overflow-trigger${open ? ' active' : ''}`}
        onClick={() => setOpen(prev => !prev)}
        aria-haspopup="menu"
        aria-expanded={open}
        title="More actions"
      >
        <i className="fas fa-ellipsis-vertical" />
      </button>

      {open && (
        <div className="chat-overflow-popover" role="menu">
          {isAdmin && (
            <button
              type="button"
              role="menuitemcheckbox"
              aria-checked={manageMode}
              className="chat-overflow-item"
              onClick={() => { onToggleManage?.(!manageMode); close() }}
              title="Manage LocalAI by chatting — install models, switch backends, edit configs."
            >
              <i className="fas fa-user-shield chat-overflow-icon" />
              <span className="chat-overflow-label">Manage mode</span>
              <span className={`chat-overflow-check${manageMode ? ' on' : ''}`}>
                {manageMode && <i className="fas fa-check" />}
              </span>
            </button>
          )}

          {(isAdmin && hasModel) && (
            <>
              <div className="chat-overflow-sep" />
              <button
                type="button"
                role="menuitem"
                className="chat-overflow-item"
                onClick={() => { onShowModelInfo?.(); close() }}
              >
                <i className="fas fa-info-circle chat-overflow-icon" />
                <span className="chat-overflow-label">Model info</span>
              </button>
              <button
                type="button"
                role="menuitem"
                className="chat-overflow-item"
                onClick={() => { onEditModel?.(); close() }}
              >
                <i className="fas fa-pen-to-square chat-overflow-icon" />
                <span className="chat-overflow-label">Edit model config</span>
              </button>
            </>
          )}

          {isAdmin && <div className="chat-overflow-sep" />}
          <button
            type="button"
            role="menuitem"
            className="chat-overflow-item"
            onClick={() => { onExport?.(); close() }}
          >
            <i className="fas fa-download chat-overflow-icon" />
            <span className="chat-overflow-label">Export as Markdown</span>
          </button>
          <button
            type="button"
            role="menuitem"
            className="chat-overflow-item chat-overflow-item-danger"
            onClick={() => { onClearHistory?.(); close() }}
          >
            <i className="fas fa-eraser chat-overflow-icon" />
            <span className="chat-overflow-label">Clear chat history</span>
          </button>
        </div>
      )}
    </div>
  )
}
