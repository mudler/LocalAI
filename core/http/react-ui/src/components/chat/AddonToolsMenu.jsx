import { useState, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { AppIcon } from '../addons/AddonBits'
import { apiUrl } from '../../utils/basePath'

// The add-ons that offer tools, as a list with one switch each, in the chat
// header. It reuses the MCP dropdown's button, badge and menu (see
// UnifiedMCPDropdown). `addons` holds only add-ons that ship tools; the page
// renders nothing when there are none.
export default function AddonToolsMenu({ addons, enabledIds, onToggle, onToggleAll }) {
  const { t } = useTranslation('chat')
  const [open, setOpen] = useState(false)
  const ref = useRef(null)

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

  if (!addons || addons.length === 0) return null
  const count = enabledIds.length
  const all = count === addons.length

  return (
    <div className="chat-mcp-dropdown chat-addons-dropdown" ref={ref} data-testid="chat-addons">
      <button
        type="button"
        className={`btn btn-sm ${count > 0 ? 'btn-primary' : 'btn-secondary'}`}
        title={t('addons.title')}
        aria-haspopup="true"
        aria-expanded={open}
        data-testid="chat-addons-button"
        onClick={() => setOpen(v => !v)}
      >
        <Icon name="puzzle" /> {t('addons.label')}
        {count > 0 && <span className="chat-mcp-badge" data-testid="chat-addons-count">{count}</span>}
      </button>
      {open && (
        <div className="chat-mcp-dropdown-menu chat-addons-menu" role="group" aria-label={t('addons.title')} data-testid="chat-addons-menu">
          <div className="chat-mcp-dropdown-header">
            <span>{t('addons.heading')}</span>
            <button type="button" className="chat-mcp-select-all" data-testid="chat-addons-all" onClick={onToggleAll}>
              {all ? t('addons.deselectAll') : t('addons.selectAll')}
            </button>
          </div>
          {addons.map(a => (
            <label key={a.id} className="chat-mcp-server-item" data-testid="chat-addon-item" data-addon={a.id}>
              <input type="checkbox" checked={enabledIds.includes(a.id)} onChange={() => onToggle(a.id)} aria-label={a.title} />
              <AppIcon src={a.icon ? apiUrl(a.icon) : ''} size={20} />
              <span className="chat-mcp-server-info">
                <span className="chat-mcp-server-name">{a.title}</span>
              </span>
            </label>
          ))}
        </div>
      )}
    </div>
  )
}
