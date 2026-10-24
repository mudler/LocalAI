/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useRef } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { apiUrl } from '../../utils/basePath'
import { StateChip, AppIcon, UnknownNotice, UpdateChip } from './AddonBits'
import AddonMenu from './AddonMenu'
import { addedByAddon, appActions, localizedText, screenshotsOf } from './addonState'
import { describeOutcome } from './errors'

// The preview of one add-on, beside the list: what it is, what it adds, and
// what to do with it. It uses what the list already carries. `children` hold
// the operator facts an administrator may open (health, trust, updates).
export default function AddonPreview({ tile, language, title, pinned, onPin, onMove, onClose, actions, onStartStop, onRemove, isAdmin, source, links = [], children }) {
  const { t } = useTranslation('addons')
  const ref = useRef(null)
  const app = tile.app
  useEffect(() => { ref.current?.focus({ preventScroll: true }) }, [tile.id])
  const onKeyDown = (e) => {
    if (e.key === 'Escape' && !e.defaultPrevented) { e.preventDefault(); onClose() }
  }
  return (
    <aside className="ad-pane" ref={ref} tabIndex={-1} data-testid="addon-detail" aria-labelledby="ad-pane-title" onKeyDown={onKeyDown}>
      <div className="ad-pane__inner" data-app={tile.id}>
      <div className="ad-pane__head">
        {tile.source === 'builtin'
          ? <span className="ad-icon ad-icon--empty" aria-hidden="true"><Icon name={tile.iconName} /></span>
          : <AppIcon src={tile.icon ? apiUrl(tile.icon) : ''} />}
        <div className="ad-pane__lead">
          <h2 className="ad-pane__title" id="ad-pane-title">{title}</h2>
          {app && (
            <div className="ad-card__chips">
              <StateChip app={app} />
              {app.update === 'update-pending' && <UpdateChip />}
            </div>
          )}
        </div>
        <AddonMenu name={title} pinned={pinned} onPin={onPin} onMove={pinned ? onMove : undefined} onRemove={onRemove} />
        <button type="button" className="ad-pane__close" aria-label={t('drawer.close')} onClick={onClose}><Icon name="close" /></button>
      </div>
      {app ? <PreviewBody app={app} language={language} actions={actions} onStartStop={onStartStop} onRemove={onRemove} isAdmin={isAdmin} source={source} links={links}>{children}</PreviewBody> : <p className="ad-pane__text">{t('drawer.builtinDetail')}</p>}
      </div>
    </aside>
  )
}

function PreviewBody({ app, language, actions, onStartStop, onRemove, isAdmin, source, links, children }) {
  const { t } = useTranslation('addons')
  const description = localizedText(app, 'description', language)
  const added = addedByAddon(app)
  const shots = screenshotsOf(app)
  const control = appActions(app)
  const startKey = `start:${app.id}:${app.generation}`
  const stopKey = `stop:${app.id}:${app.generation}`
  const pending = Boolean(actions.unknown[app.id])
  const working = actions.state[startKey]?.phase === 'working' || actions.state[stopKey]?.phase === 'working'
  const locked = working || pending
  const result = actions.state[startKey]?.phase !== 'working' ? actions.state[startKey] : null
  const stopResult = actions.state[stopKey]?.phase !== 'working' ? actions.state[stopKey] : null
  const outcome = result || stopResult
  return (
    <div className="ad-pane__body">
      {description && <p className="ad-pane__text">{description}</p>}
      {shots.length > 0 && (
        <ul className="ad-shots" data-testid="addon-shots" aria-label={t('preview.screenshots')}>
          {shots.map(src => <li key={src}><img src={apiUrl(src)} alt="" loading="lazy" decoding="async" /></li>)}
        </ul>
      )}
      <section className="ad-pane__section" aria-labelledby="ad-pane-adds">
        <h3 className="ad-section__title" id="ad-pane-adds">{t('preview.adds')}</h3>
        <ul className="ad-adds">
          {added.page && <li data-testid="app-page"><Icon name="file" aria-hidden="true" /> <span>{t('preview.page')}</span></li>}
          {added.tools > 0 && (
            <li data-testid="app-tools">
              <Icon name="wrench" aria-hidden="true" />
              <span>
                {t('card.chatTools', { count: added.tools })}
                {added.toolNames.length > 0 && <>{': '}<span className="dk-mono">{added.toolNames.join(', ')}</span>{added.toolsMore > 0 ? ` ${t('apps.toolsMore', { count: added.toolsMore })}` : ''}</>}
              </span>
            </li>
          )}
          {!added.page && added.tools === 0 && <li className="ad-quiet">{t('preview.addsNothing')}</li>}
        </ul>
        {!added.page && <p className="ad-quiet">{t('apps.headless')}</p>}
        {links.length > 0 && (
          <div className="ad-pane__acts">
            {links.map(link => <Link key={link.path} className="dk-btn dk-btn--ghost dk-btn--sm" to={link.path}>{link.label}</Link>)}
          </div>
        )}
      </section>
      <div className="ad-pane__acts">
        {app.mainPage && (
          <Link className="dk-btn dk-btn--primary dk-btn--sm" to={app.mainPage}>
            <Icon name="external-link" /> {t('apps.open')}
          </Link>
        )}
        {control.start && (
          <button type="button" className={`dk-btn dk-btn--sm${app.mainPage ? '' : ' dk-btn--primary'}`} disabled={locked} onClick={() => onStartStop(app, 'start')}>
            <Icon name={working ? 'spinner' : 'toggle-on'} spin={working} /> {t('apps.start')}
          </button>
        )}
        {control.stop && (
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" disabled={locked} onClick={() => onStartStop(app, 'stop')}>
            <Icon name={working ? 'spinner' : 'toggle-off'} spin={working} /> {t('apps.stop')}
          </button>
        )}
        {onRemove && (
          <button type="button" className="dk-btn dk-btn--danger dk-btn--sm" disabled={locked} onClick={onRemove} data-testid="pane-remove">
            <Icon name="trash" /> {t('card.remove')}
          </button>
        )}
      </div>
      {working && <p className="ad-card__note" role="status">{t('apps.working')}</p>}
      {pending
        ? <UnknownNotice addonId={app.id} actions={actions} className="ad-card__note" />
        : outcome && outcome.outcome && describeOutcome(outcome.outcome, t, outcome.error) && (
          <p className="ad-card__note" data-tone="error" role="alert">{describeOutcome(outcome.outcome, t, outcome.error)}</p>
        )}
      <dl className="dk-kv ad-kv ad-pane__facts" data-testid="preview-facts">
        <dt>{t('preview.version')}</dt><dd>{app.version}</dd>
        {isAdmin && <><dt>{t('trust.library')}</dt><dd data-testid="preview-source">{source ? `${source.sourceId || '—'}${source.channel ? ` · ${source.channel}` : ''}` : t('preview.sourceUnknown')}</dd></>}
        <dt>{t('preview.id')}</dt><dd className="dk-mono">{app.id}</dd>
      </dl>
      {children}
    </div>
  )
}
