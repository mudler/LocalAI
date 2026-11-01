import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { chatsApi } from '../../utils/api'
import Icon from '../Icon'

// The sub-agents a chat started: each is its own conversation that the server
// keeps and runs, also in the background while the parent goes on. The list
// comes from GET /api/chats/{id}/agents and is read again when one starts or
// ends (tick). A running one can be stopped; its transcript opens in place.
// The chip shows only when the chat has sub-agents. It reuses the popover of
// the tools menu (SwitchMenu).
export default function SubAgentsMenu({ chatId, tick = 0 }) {
  const { t } = useTranslation('chats')
  const [list, setList] = useState({ sub_agents: [], running: 0, background_limit: 0 })
  const [open, setOpen] = useState(false)
  const [shown, setShown] = useState({}) // id -> transcript lines, or 'loading'
  const [error, setError] = useState('')
  const ref = useRef(null)

  const load = useCallback(async () => {
    if (!chatId) return
    try {
      setList(await chatsApi.subAgents(chatId))
    } catch {
      setList({ sub_agents: [], running: 0, background_limit: 0 })
    }
  }, [chatId])
  useEffect(() => { setShown({}); setError('') }, [chatId])
  useEffect(() => { load() }, [load, tick])

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

  const runs = list.sub_agents || []
  if (!chatId || runs.length === 0) return null

  const stop = async (id) => {
    setError('')
    try {
      await chatsApi.stopSubAgent(chatId, id)
    } catch (e) {
      setError(e.message)
    }
    load()
  }
  const toggle = async (id) => {
    if (shown[id]) { setShown(s => ({ ...s, [id]: undefined })); return }
    setShown(s => ({ ...s, [id]: 'loading' }))
    try {
      const res = await chatsApi.subAgentMessages(chatId, id)
      const lines = (res.messages || []).filter(m => m.role === 'user' || m.role === 'assistant')
      setShown(s => ({ ...s, [id]: lines }))
    } catch (e) {
      setShown(s => ({ ...s, [id]: undefined }))
      setError(e.message)
    }
  }
  const state = (r) => (r.running ? t('subAgents.running') : t(`status.${r.last_turn || 'done'}`, r.last_turn || ''))

  return (
    <div className="chat-mcp-dropdown chat-addons-dropdown" ref={ref} data-testid="chat-subagents">
      <button
        type="button"
        className={`btn btn-sm ${list.running > 0 ? 'btn-primary' : 'btn-secondary'}`}
        aria-haspopup="true"
        aria-expanded={open}
        title={t('subAgents.title')}
        data-testid="chat-subagents-button"
        onClick={() => setOpen(v => !v)}
      >
        <Icon name="robot" /> {t('subAgents.label')}
        <span className="chat-mcp-badge">{list.running > 0 ? list.running : runs.length}</span>
      </button>
      {open && (
        <div className="chat-mcp-dropdown-menu chat-addons-menu" role="group" aria-label={t('subAgents.title')}>
          <div className="chat-mcp-dropdown-header">
            <span>{t('subAgents.heading', { running: list.running, limit: list.background_limit })}</span>
          </div>
          <div className="cx-subagents__list">
            {runs.map((r) => (
              <div key={r.id} className="cx-subagents__row" data-testid="chat-subagent">
                <span className="cx-subagents__title" title={r.title}>{r.title || t('subAgents.untitled')}</span>
                <span className="cx-subagents__acts">
                  <button type="button" className="cx-link" aria-expanded={!!shown[r.id]} onClick={() => toggle(r.id)}>
                    {shown[r.id] ? t('subAgents.hide') : t('subAgents.show')}
                  </button>
                  {r.running && (
                    <button type="button" className="cx-link cx-link--danger" onClick={() => stop(r.id)}>{t('subAgents.stop')}</button>
                  )}
                </span>
                <span className="cx-subagents__state">{state(r)}</span>
                {shown[r.id] && (
                  <div className="cx-subagents__log" data-testid="chat-subagent-log">
                    {shown[r.id] === 'loading'
                      ? t('subAgents.loading')
                      : shown[r.id].length === 0
                        ? t('subAgents.empty')
                        : shown[r.id].map(m => `${m.role === 'user' ? t('subAgents.task') : t('subAgents.answer')}: ${m.text}`).join('\n\n')}
                  </div>
                )}
              </div>
            ))}
          </div>
          {error && <div className="chat-mcp-dropdown-error" role="alert">{error}</div>}
        </div>
      )}
    </div>
  )
}
