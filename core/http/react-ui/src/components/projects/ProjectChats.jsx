/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { EmptyState } from '../ui'
import { whenAgo } from '../../utils/projects'

export const chatPath = (chat) => `/app/chats/${encodeURIComponent(chat.id)}`

// The rows of chats of a project, newest first as the server sends them.
export function ChatRows({ chats }) {
  const { t, i18n } = useTranslation('projects')
  return (
    <ul className="pj-list" data-testid="project-chat-list">
      {chats.map(c => (
        <li key={c.id} className="pj-line pj-line--chat" data-testid="project-chat-row">
          <span className="pj-tile pj-tile--sm" aria-hidden="true"><Icon name="chat" /></span>
          <Link className="pj-line__name" to={chatPath(c)}>{c.title || t('chats.untitled')}</Link>
          <span className="pj-muted pj-line__when">{whenAgo(c.updated, i18n.language)}</span>
        </li>
      ))}
    </ul>
  )
}

// The Chats tab: every chat that has the project as its home.
export default function ProjectChats({ chats }) {
  const { t } = useTranslation('projects')
  return (
    <section className="pj-section" data-testid="project-chats">
      <h2 className="pj-h2">{t('chats.title')}</h2>
      {chats.length === 0
        ? <EmptyState variant="no-results" icon="chat" title={t('chats.empty')} />
        : <ChatRows chats={chats} />}
    </section>
  )
}
