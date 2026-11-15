/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { sharedPath } from '../../utils/sharedProjects'
import './projectchat.css'

const projectPath = (id) => `/app/projects/${encodeURIComponent(id)}`

// The home project of a chat, as a chip in its header that opens the project.
// The same chips also sit in a strip under the header on a narrow screen, where
// the header has no room (testId is then left out).
export function ProjectChip({ project, testId = 'chat-project-chip' }) {
  const { t } = useTranslation('projects')
  return (
    <Link className="pjc-chip" to={projectPath(project.id)} title={t('chat.open', { name: project.name })} data-testid={testId}>
      <Icon name="layers" />
      <span className="pjc-chip__name">{project.name}</span>
    </Link>
  )
}

// A project the chat pulls in: the lock says it is read-only at its pulled path.
// An archived or unavailable project is not mounted, so it has no lock and says
// that instead. A project shared with you opens its shared page and says who
// shared it and what you may do in its files; a shared one the list does not name is not
// a link and says it is not available.
export function PulledChip({ project, testId = 'chat-pulled-chip' }) {
  const { t } = useTranslation('projects')
  const state = project.state || 'mounted'
  if (project.shared) {
    const access = t(project.mode === 'write' ? 'share.canEdit' : 'share.readOnly')
    if (state === 'unlisted') {
      return (
        <span className="pjc-chip pjc-chip--pulled" title={t('chat.unlistedTitle')} data-testid={testId} data-state="unlisted">
          <Icon name="alert-circle" />
          <span className="pjc-chip__name">{t('chat.unlistedName')}</span>
        </span>
      )
    }
    const tag = state === 'mounted' ? access : t(state === 'archived' ? 'chat.pulledArchived' : 'chat.pulledUnavailable')
    return (
      <Link className="pjc-chip pjc-chip--pulled" to={sharedPath(project.id)} title={t('chat.sharedTitle', { name: project.name, owner: project.owner, access })} data-testid={testId} data-state={state} data-shared="true">
        <Icon name={state === 'mounted' ? 'users' : 'alert-circle'} />
        <span className="pjc-chip__name">{project.name}</span>
        <span className="sr-only">{t('share.sharedBy', { name: project.owner })}</span>
        <span className="pjc-chip__tag">{tag}</span>
      </Link>
    )
  }
  const tag = { mounted: t('chat.pulledBadge'), archived: t('chat.pulledArchived'), unavailable: t('chat.pulledUnavailable') }[state]
  const title = { mounted: t('chat.pulledTitle', { name: project.name }), archived: t('chat.pulledArchivedTitle', { name: project.name }), unavailable: t('chat.pulledUnavailableTitle', { name: project.name }) }[state]
  return (
    <Link className="pjc-chip pjc-chip--pulled" to={projectPath(project.id)} title={title} data-testid={testId} data-state={state}>
      <Icon name={state === 'mounted' ? 'lock' : 'alert-circle'} />
      <span className="pjc-chip__name">{project.name}</span>
      <span className="pjc-chip__tag">{tag}</span>
    </Link>
  )
}

// "+N": the pulled projects the header has no room for. It opens the chat
// settings, whose Project section lists every one of them.
export function MoreChip({ count, onClick }) {
  const { t } = useTranslation('projects')
  return (
    <button type="button" className="pjc-chip pjc-chip--more" title={t('chat.moreTitle', { count })} aria-label={t('chat.moreTitle', { count })} data-testid="chat-pulled-more" onClick={onClick}>
      {t('chat.moreChip', { count })}
    </button>
  )
}
