/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { Button, Callout } from '../ui'

// The note under the chat header while projects are pulled in. It says what
// the pull does and what it does not: the files and the memory are mounted
// read-only at the pulled path, and the chat still reaches the same files
// through the home folder, so the pull is not a protection.
export default function PulledProjects({ projects, onManage, onRemove, onRetry }) {
  const { t } = useTranslation(['projects', 'common'])
  // Only a project that is mounted this turn is described as read-only: an
  // archived or unavailable one is not mounted, and its chip says so.
  const unlisted = projects.filter(p => p.state === 'unlisted')
  projects = projects.filter(p => p.state === 'mounted')
  const shared = projects.filter(p => p.shared)
  return (
    <>
      {projects.length > 0 && (
        <Mounted projects={projects} shared={shared} onManage={onManage} t={t} />
      )}
      {unlisted.length > 0 && (
        <Callout
          tone="warn"
          title={t('chat.unlistedTitle')}
          testId="chat-pulled-unlisted"
          className="pjc-callout"
          actions={(onRetry || onRemove) ? (
            <>
              {onRetry && <Button size="sm" onClick={onRetry}>{t('common:actions.retry')}</Button>}
              {onRemove && <Button size="sm" variant="ghost" onClick={() => onRemove(unlisted.map(p => p.id))}>{t('chat.removeUnlisted')}</Button>}
            </>
          ) : null}
        >
          {t('chat.unlistedBody', { count: unlisted.length })}
        </Callout>
      )}
    </>
  )
}

function Mounted({ projects, shared, onManage, t }) {
  return (
    <Callout
      icon="lock"
      title={t('chat.calloutTitle', { count: projects.length, names: projects.map(p => p.name).join(', ') })}
      testId="chat-pulled-callout"
      className="pjc-callout"
      actions={onManage ? <Button size="sm" onClick={onManage}>{t('chat.manage')}</Button> : null}
    >
      {t('chat.calloutBody', { count: projects.length })}
      {shared.length > 0 && (
        <span data-testid="chat-pulled-shared-note">
          {' '}
          {shared.map(p => t(p.mode === 'write' ? 'chat.sharedNoteWrite' : 'chat.sharedNoteRead', { name: p.name, owner: p.owner })).join(' ')}
          {' '}
          {t('chat.sharedNoteTail', { count: shared.length })}
        </span>
      )}
    </Callout>
  )
}
