import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'

// The saved agent a new chat starts with, as a chip of the composer. An agent
// gives the chat its instructions and exactly the tools it grants; its model,
// when it names one, replaces the model of the chip. Once the chat exists its
// agent does not change, so the chip only shows it (readOnly).
export default function AgentPicker({ agents, value, onChange, readOnly = false }) {
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

  const picked = agents.find((a) => a.id === value)
  if (readOnly) {
    if (!picked) return null
    return (
      <span className="home-chip" data-testid="chat-agent" title={t('agent.fixed')}>
        <Icon name="robot" /> <span className="home-chip__text">{picked.name}</span>
      </span>
    )
  }
  if (agents.length === 0) return null

  const choose = (id) => { onChange(id); setOpen(false) }
  return (
    <div className="chat-mcp-dropdown chat-addons-dropdown" ref={ref} data-testid="chat-agent-picker">
      <button
        type="button"
        className={`btn btn-sm ${picked ? 'btn-primary' : 'btn-secondary'}`}
        title={t('agent.title')}
        aria-haspopup="true"
        aria-expanded={open}
        onClick={() => setOpen(v => !v)}
      >
        <Icon name="robot" /> {picked ? picked.name : t('agent.label')}
      </button>
      {open && (
        <div className="chat-mcp-dropdown-menu chat-addons-menu" role="radiogroup" aria-label={t('agent.title')} data-testid="chat-agent-menu">
          <div className="chat-mcp-dropdown-header"><span>{t('agent.heading')}</span></div>
          <p className="chat-addons-note">{t('agent.note')}</p>
          <label className="chat-mcp-server-item">
            <input type="radio" name="chat-agent" checked={!picked} onChange={() => choose('')} />
            <span className="chat-mcp-server-info"><span className="chat-mcp-server-name">{t('agent.none')}</span></span>
          </label>
          {agents.map((a) => (
            <label key={a.id} className="chat-mcp-server-item" title={a.description || undefined}>
              <input type="radio" name="chat-agent" checked={a.id === value} onChange={() => choose(a.id)} aria-label={a.name} />
              <span className="chat-mcp-server-info">
                <span className="chat-mcp-server-name">{a.name}</span>
                {a.model && <span className="chat-mcp-server-tools">{a.model}</span>}
              </span>
            </label>
          ))}
        </div>
      )}
    </div>
  )
}
