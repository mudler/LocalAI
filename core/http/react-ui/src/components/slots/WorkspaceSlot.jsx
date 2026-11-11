/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import Icon from '../Icon'
import ConfirmDialog from '../ConfirmDialog'
import SlotTile from './SlotTile'
import { useWellKnown } from '../../hooks/useWellKnown'
import { useAuth } from '../../context/AuthContext'
import { itemLabel, readSlots, slotKey, toolPrompt } from '../../utils/slots'
import './slots.css'

// ToolButton runs one tool with fixed arguments. It never calls the tool by
// itself: the person approves first (this dialog), then the request goes to
// the chat as a visible message that names the tool and its arguments, and the
// tool's own approval in the chat still applies. Outside a chat (the sidebar)
// it opens a new chat with the request as a draft, which the person sends.
function ToolButton({ item, onTool }) {
  const { t } = useTranslation('workspace')
  const navigate = useNavigate()
  const [asking, setAsking] = useState(false)
  const label = itemLabel(item)
  const prompt = toolPrompt(item)
  const args = item.arguments && Object.keys(item.arguments).length ? JSON.stringify(item.arguments) : ''
  const run = () => {
    setAsking(false)
    if (onTool) onTool(prompt)
    else navigate('/app/chats', { state: { draft: prompt } })
  }
  return (
    <>
      <button type="button" className="ws-chip" onClick={() => setAsking(true)} title={t('wellKnown.slots.tool.title', { tool: item.tool })} data-testid="slot-tool" data-tool={item.tool}>
        <Icon name={item.icon || 'wrench'} className="ws-chip__icon" />
        <span className="ws-chip__label">{label}</span>
      </button>
      {asking && createPortal(
        <ConfirmDialog
          open
          title={t('wellKnown.slots.tool.confirmTitle', { label })}
          message={(
            <div className="ws-approve" data-testid="slot-tool-approve">
              <p>{t(onTool ? 'wellKnown.slots.tool.confirmChat' : 'wellKnown.slots.tool.confirmDraft')}</p>
              <dl className="ws-approve__facts">
                <dt>{t('wellKnown.slots.tool.tool')}</dt><dd><code>{item.tool}</code></dd>
                {args && <><dt>{t('wellKnown.slots.tool.arguments')}</dt><dd><code>{args}</code></dd></>}
              </dl>
              <p className="ws-approve__note">{t('wellKnown.slots.tool.stillAsks')}</p>
            </div>
          )}
          confirmLabel={t('wellKnown.slots.tool.run')}
          onConfirm={run}
          onCancel={() => setAsking(false)}
        />,
        document.body,
      )}
    </>
  )
}

// A command of commands.json. The commands come with their own slice; until
// then a command item is kept in the file but shows nothing. resolveCommand
// is the seam: it returns the command to run, or null.
const resolveCommand = () => null

// WorkspaceSlot draws one slot of ui.json: its page tiles, tool buttons and
// commands, left to right, in a strip about 40 px high. Nothing shows when the
// slot is empty, so without ui.json slots the app looks as before.
//
//   name     chatHeader, sidebarFooter or composerToolbar
//   onTool   sends the request of an approved tool button to the open chat;
//            without it the button opens a new chat with a draft
export default function WorkspaceSlot({ name, onTool }) {
  const { t } = useTranslation('workspace')
  const { hasFeature } = useAuth()
  const on = hasFeature('chats')
  const { doc } = useWellKnown('ui', { enabled: on })
  const items = useMemo(() => readSlots(doc?.data)[name] || [], [doc, name])
  const pagesOn = on && hasFeature('pages')
  const shown = items.filter((it) => (it.type === 'page' ? pagesOn : it.type === 'tool' ? true : !!resolveCommand(it)))
  if (!on || shown.length === 0) return null
  return (
    <div className={`ws-slot ws-slot--${name}`} role="group" aria-label={t(`wellKnown.slots.names.${name}`)} data-testid={`slot-${name}`}>
      {shown.map((it) => {
        const key = slotKey(it)
        if (it.type === 'page') return <SlotTile key={key} item={it} slot={name} />
        if (it.type === 'tool') return <ToolButton key={key} item={it} onTool={onTool} />
        return null
      })}
    </div>
  )
}
