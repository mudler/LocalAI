import { useState, useEffect, useRef, useId } from 'react'
import Icon from '../Icon'

// A chip of the composer that opens a list of switches for this chat, in
// groups: the tools (add-ons, MCP servers of the model, your MCP connectors)
// or the skills. It reuses the MCP dropdown's button, badge and menu. A row the
// chat cannot use now is shown off, with the reason. The control renders
// nothing when there is nothing to switch.
//
// groups: [{ key, title, rows: [{ key, label, title, on, usable, reason, note, warn, onToggle }] }]
// note is a line under the name (warn draws it as a warning).
// notice is a block over the groups, for something that holds for the whole
// chat (the chat cannot run commands); the menu shows for it even with no rows.
export default function SwitchMenu({ icon, label, title, heading, note, notice, groups, error, footer, testId }) {
  const [open, setOpen] = useState(false)
  const ref = useRef(null)
  const noteId = useId()

  useEffect(() => {
    if (!open) return undefined
    const onDown = (e) => { if (ref.current && !ref.current.contains(e.target)) setOpen(false) }
    const onKey = (e) => { if (e.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  const rows = groups.flatMap((g) => g.rows)
  if (rows.length === 0 && !notice) return null
  const count = rows.filter((r) => r.usable !== false && r.on).length
  const titled = groups.filter((g) => g.rows.length > 0).length > 1

  return (
    <div className="chat-mcp-dropdown chat-addons-dropdown" ref={ref} data-testid={testId}>
      <button
        type="button"
        className={`btn btn-sm ${count > 0 ? 'btn-primary' : 'btn-secondary'}`}
        title={title}
        aria-haspopup="true"
        aria-expanded={open}
        aria-describedby={open && note ? noteId : undefined}
        data-testid={`${testId}-button`}
        onClick={() => setOpen(v => !v)}
      >
        <Icon name={icon} /> {label}
        {count > 0 && <span className="chat-mcp-badge" data-testid={`${testId}-count`}>{count}</span>}
      </button>
      {open && (
        <div className="chat-mcp-dropdown-menu chat-addons-menu" role="group" aria-label={title} data-testid={`${testId}-menu`}>
          <div className="chat-mcp-dropdown-header"><span>{heading}</span></div>
          {note && <p className="chat-addons-note" id={noteId}>{note}</p>}
          {notice}
          {groups.filter((g) => g.rows.length > 0).map((g) => (
            <div key={g.key} role="group" aria-label={g.title}>
              {titled && <div className="chat-mcp-dropdown-header chat-switch-group"><span>{g.title}</span></div>}
              {g.rows.map((r) => (
                <label key={r.key} className={`chat-mcp-server-item${r.usable === false ? ' chat-mcp-server-item--error' : ''}`} title={r.title} data-testid={`${testId}-item`}>
                  <input
                    type="checkbox"
                    checked={r.usable !== false && r.on}
                    disabled={r.usable === false}
                    onChange={(e) => r.onToggle(e.target.checked)}
                    aria-label={r.label}
                  />
                  <span className="chat-mcp-server-info">
                    <span className="chat-mcp-server-name">{r.label}</span>
                    {r.usable === false && r.reason && <span className="chat-mcp-server-error">{r.reason}</span>}
                    {r.note && <span className={r.warn ? 'chat-mcp-server-error' : 'chat-mcp-server-tools'} data-testid={`${testId}-note`}>{r.note}</span>}
                  </span>
                </label>
              ))}
            </div>
          ))}
          {error && <div className="chat-mcp-dropdown-error" role="alert">{error}</div>}
          {footer}
        </div>
      )}
    </div>
  )
}
