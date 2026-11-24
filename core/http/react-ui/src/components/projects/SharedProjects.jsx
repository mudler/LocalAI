/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button, StatusChip } from '../ui'
import { sharedAccess, sharedPath, sharedUsable } from '../../utils/sharedProjects'

// The access chip of a shared project: what the person may do in its files.
export function SharedAccess({ mode }) {
  const { t } = useTranslation('projects')
  return sharedAccess(mode) === 'write'
    ? <StatusChip tone="accent" icon="edit" label={t('share.canEdit')} />
    : <StatusChip tone="idle" icon="eye" label={t('share.readOnly')} />
}

// The words under an unavailable shared project: why it cannot be used now.
export function unavailableText(p, t) {
  if (p.status === 'archived') return t('share.archivedNote')
  return p.error || t('share.brokenNote')
}

// The "Shared with me" section of the Projects list: the projects other
// people share with the person. Everything in a row is the owner's text, drawn
// as text; the ref is only used in the link. An archived or broken project is
// listed as unavailable and cannot be opened.
export default function SharedProjects({ projects }) {
  const { t } = useTranslation('projects')
  if (!projects || projects.length === 0) return null
  return (
    <section className="pj-section" data-testid="projects-shared" aria-labelledby="projects-shared-title">
      <div className="pj-head">
        <h2 className="pj-h2" id="projects-shared-title">{t('share.sectionTitle')}</h2>
        <span className="pj-muted">{t('share.sectionHint')}</span>
      </div>
      <ul className="pj-rows" data-testid="projects-shared-list">
        {projects.map(p => {
          const usable = sharedUsable(p)
          return (
            <li key={p.ref} className={`pj-row${usable ? '' : ' pj-row--unavailable'}`} data-testid="shared-row" data-project={p.name} data-status={p.status}>
              <span className="pj-tile" aria-hidden="true"><Icon name="layers" /></span>
              <div className="pj-row__main">
                {usable
                  ? <Link className="pj-row__name" to={sharedPath(p.ref)}>{p.name}</Link>
                  : <span className="pj-row__name">{p.name}</span>}
                {p.description && <span className="pj-row__desc">{p.description}</span>}
                {!usable && (
                  <span className="pj-row__error pj-row__error--quiet" role="note" data-testid="shared-row-note">
                    <Icon name="warning" />{unavailableText(p, t)}
                  </span>
                )}
              </div>
              <div className="pj-row__meta">
                <span className="pj-muted"><Icon name="user" />{t('share.sharedBy', { name: p.owner_name })}</span>
                {usable ? <SharedAccess mode={p.mode} /> : <StatusChip tone="warn" icon="alert-circle" label={t('share.unavailable')} />}
              </div>
              <div className="pj-row__end">
                {usable
                  ? <Button size="sm" to={sharedPath(p.ref)} aria-label={t('share.openNamed', { name: p.name })}>{t('actions.open')}</Button>
                  : <Button size="sm" disabled aria-label={t('share.openNamed', { name: p.name })}>{t('actions.open')}</Button>}
              </div>
            </li>
          )
        })}
      </ul>
    </section>
  )
}
