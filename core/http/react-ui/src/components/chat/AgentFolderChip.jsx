import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import { Link } from 'react-router-dom'
import Icon from '../Icon'
import { folderName, folderRows, folderState } from '../../utils/chatAgents'

// The folder that a chat with this agent works in, as a chip of the composer
// next to the agent: its name, with a lock when the chat can only read it. A
// folder that is gone, or was never chosen, shows a warning that opens the
// agent's editor. An agent on all the files shows nothing.
export default function AgentFolderChip({ agent }) {
  const { t } = useTranslation('chat')
  const st = folderState(agent)
  if (st === 'none') return null
  if (st !== 'ok') {
    return (
      <Link className="home-chip" to={`/app/agents/${encodeURIComponent(agent.id)}/edit`} data-testid="chat-agent-folder" data-state={st} title={t('agentFolder.missing')}>
        <Icon name="warning" /> <span className="home-chip__text">{t('agentFolder.chipMissing')}</span>
      </Link>
    )
  }
  const rows = folderRows(agent)
  const read = rows.every(f => (f.effective || f.mode) !== 'write')
  const name = (folderName(rows[0].path) || t('agentFolder.home')) + (rows.length > 1 ? ` +${rows.length - 1}` : '')
  const paths = rows.map(f => (f.path === '/' ? t('agentFolder.home') : f.path)).join(', ')
  return (
    <span className="home-chip" data-testid="chat-agent-folder" data-state="ok" title={t(read ? 'agentFolder.titleRead' : 'agentFolder.titleWrite', { path: paths })}>
      <Icon name={read ? 'lock' : 'folder'} /> <span className="home-chip__text">{t('agentFolder.chip', { name, mode: t(read ? 'agentFolder.read' : 'agentFolder.write') })}</span>
    </span>
  )
}
