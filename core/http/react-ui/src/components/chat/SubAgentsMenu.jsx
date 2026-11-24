/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { chatsApi } from '../../utils/api'
import Icon from '../Icon'
import { SwitchHead, SwitchPopover, SwitchStatus } from './SwitchList'
import { isWait, loopSummary } from '../../utils/chatLoops'

// The sub-agents a chat started: each is its own conversation that the server
// keeps and runs, also in the background while the parent goes on. The list
// comes from GET /api/chats/{id}/agents and is read again when one starts or
// ends (tick). A running one can be stopped; its transcript opens in place.
// The chip shows only when the chat has sub-agents. It is the popover of the
// Tools menu (SwitchPopover): the composer chip, with a green dot and the
// number running while any run, else the number of sub-agents.
export default function SubAgentsMenu({ chatId, tick = 0 }) {
  const { t } = useTranslation('chats')
  const [list, setList] = useState({ sub_agents: [], running: 0, background_limit: 0 })
  const [shown, setShown] = useState({}) // id -> transcript lines, or 'loading'
  const [error, setError] = useState('')

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
    <SwitchPopover
      icon="robot"
      label={t('subAgents.label')}
      title={t('subAgents.title')}
      count={list.running > 0 ? list.running : runs.length}
      dot={list.running > 0}
      testId="chat-subagents"
      menuClassName="swl-menu--wide"
    >
      <SwitchHead title={t('subAgents.heading', { running: list.running, limit: list.background_limit })} />
      <div className="swl-list cx-subagents__list">
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
            {r.loop && (
              <span className="cx-subagents__loop" data-testid="chat-subagent-loop">
                <Icon name={isWait(r.loop) ? 'hourglass' : 'repeat'} className="cx-subagents__loop-icon" />
                <span>
                  {isWait(r.loop)
                    ? t('subAgents.waiting', { what: r.loop.watch.reason || r.loop.watch.condition })
                    : t('subAgents.looping')}
                  {' · '}{loopSummary(t, r.loop, Date.now(), false)}
                </span>
              </span>
            )}
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
      <SwitchStatus error={error} failed={error} testId="chat-subagents-error" />
    </SwitchPopover>
  )
}
