/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import ConfirmDialog from '../ConfirmDialog'
import { chatChangesApi, chatMemoryApi } from '../../utils/api'
import { downloadFrom } from '../../utils/chatThread'
import { agentNoteCount } from '../../utils/memoryFiles'
import { deleteAgentWithUndo } from './agentUndo'
import './agentmemory.css'

const CHOICES = ['keep', 'export', 'delete']

// The dialog of an agent that has notes of its own: keep them, export them and
// delete them, or delete them. Keep is the default.
function NotesDialog({ pending, onConfirm, onCancel }) {
  const { t } = useTranslation('chats')
  const [choice, setChoice] = useState('keep')
  return (
    <ConfirmDialog
      open
      danger
      title={t('agentMemory.delete.title', { name: pending.agent.name })}
      confirmLabel={t('agentMemory.delete.confirm')}
      onConfirm={() => onConfirm(choice)}
      onCancel={onCancel}
      message={(
        <div className="am-del" data-testid="agent-delete-notes">
          <p className="am-del__ask">{t('agentMemory.delete.ask', { count: pending.count })}</p>
          <fieldset className="am-del__opts">
            <legend className="sr-only">{t('agentMemory.delete.legend')}</legend>
            {CHOICES.map((c) => (
              <label key={c} className={`am__card${choice === c ? ' am__card--on' : ''}`} data-testid={`agent-delete-notes-${c}`}>
                <input type="radio" name="agent-delete-notes" value={c} checked={choice === c} onChange={() => setChoice(c)} />
                <span className="am__text">
                  <span className="am__title">{t(`agentMemory.delete.${c}.title`)}{c === 'keep' && <span className="am__tag">{t('agentMemory.delete.default')}</span>}</span>
                  <span className="am__body">{t(`agentMemory.delete.${c}.body`)}</span>
                </span>
              </label>
            ))}
          </fieldset>
        </div>
      )}
    />
  )
}

// Deleting an agent. An agent with no notes of its own is deleted as before,
// with the Undo window and no question. An agent with notes asks first what
// to do with them. Export downloads the notes as a zip file before the delete;
// delete moves them to the Trash; keep leaves them on the Memory page under
// Deleted agents. `ask(agent, after)` starts it; `dialog` goes in the page.
export function useAgentDelete({ addToast, t, onDeleted }) {
  const [pending, setPending] = useState(null)

  const run = useCallback((agent, memory, after) => {
    deleteAgentWithUndo(agent, { addToast, t, onDeleted, memory })
    after?.()
  }, [addToast, t, onDeleted])

  const ask = useCallback(async (agent, after) => {
    let count = 0
    try {
      count = agentNoteCount((await chatChangesApi.contextFiles())?.groups, agent.id)
    } catch {
      // Notes that cannot be counted are not a reason to block the delete: the
      // default keeps them.
    }
    if (count === 0) { run(agent, undefined, after); return }
    setPending({ agent, count, after })
  }, [run])

  const dialog = pending && (
    <NotesDialog
      pending={pending}
      onCancel={() => setPending(null)}
      onConfirm={(choice) => {
        const { agent, after } = pending
        setPending(null)
        if (choice === 'export') downloadFrom(chatMemoryApi.exportUrl('agent', agent.id))
        run(agent, choice === 'keep' ? 'keep' : 'delete', after)
      }}
    />
  )
  return { ask, dialog }
}
