/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useRef } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button, Callout } from '../ui'
import { apiUrl } from '../../utils/basePath'
import { StateChip, AppIcon, BuiltinIcon, UnknownNotice, UpdateChip, VersionText } from './AddonBits'
import AddonMenu from './AddonMenu'
import { addedByAddon, appActions, attentionOf, localizedText, screenshotsOf } from './addonState'
import { updateView, upgradeAvailability } from './updateState'
import { describeOutcome } from './errors'

// The details of one add-on, beside the list. From the top: who it is and its
// state; what needs a decision now (a failure, an update); what to do with it
// (Open, Turn on or off, Pin); what it can do; then, for an administrator, the
// operator facts in `children` (trust, health, updates, compatibility, storage
// and data). Remove sits last, apart from everything else.
export default function AddonPreview({ tile, language, title, pinned, onPin, onMove, onClose, actions, onStartStop, onRemove, isAdmin, source, links = [], upgrade, children }) {
  const { t } = useTranslation('addons')
  const ref = useRef(null)
  const app = tile.app
  useEffect(() => { ref.current?.focus({ preventScroll: true }) }, [tile.id])
  const onKeyDown = (e) => {
    if (e.key === 'Escape' && !e.defaultPrevented) { e.preventDefault(); onClose() }
  }
  const update = isAdmin && source ? updateView(source.update) : null
  return (
    <aside className="ad-pane" ref={ref} tabIndex={-1} data-testid="addon-detail" aria-labelledby="ad-pane-title" onKeyDown={onKeyDown}>
      <div className="ad-pane__inner" data-app={tile.id}>
        <div className="ad-pane__head">
          {tile.source === 'builtin'
            ? <BuiltinIcon name={tile.iconName} size={44} />
            : <AppIcon src={tile.icon ? apiUrl(tile.icon) : ''} id={tile.id} name={title} size={44} />}
          <div className="ad-pane__lead">
            <h2 className="ad-pane__title" id="ad-pane-title">{title}</h2>
            {app && (
              <div className="ad-pane__meta">
                <StateChip app={app} />
                {(app.update === 'update-pending' || update?.state === 'update-available') && <UpdateChip version={update?.state === 'update-available' ? update.version : ''} />}
                <span className="ad-pane__where">
                  <VersionText version={app.version} />
                  {isAdmin && source && source.sourceId ? <> · {t('meta.library', { library: source.sourceId })}</> : null}
                </span>
              </div>
            )}
          </div>
          <AddonMenu name={title} pinned={pinned} onPin={onPin} onMove={pinned ? onMove : undefined} onRemove={onRemove} />
          <Button variant="ghost" size="sm" icon="close" className="ad-pane__close" aria-label={t('drawer.close')} onClick={onClose} />
        </div>
        {app
          ? <PreviewBody app={app} title={title} language={language} pinned={pinned} onPin={onPin} actions={actions} onStartStop={onStartStop} onRemove={onRemove} isAdmin={isAdmin} source={source} update={update} upgrade={upgrade} links={links}>{children}</PreviewBody>
          : <p className="ad-pane__text ad-pane__text--pad">{t('drawer.builtinDetail')}</p>}
      </div>
    </aside>
  )
}

function PreviewBody({ app, title, language, pinned, onPin, actions, onStartStop, onRemove, isAdmin, source, update, upgrade, links, children }) {
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
  const attention = attentionOf(app, source)
  const restarts = Number(source?.restarts) || 0
  const canUpgrade = Boolean(upgrade && source && update?.state === 'update-available' && upgradeAvailability(source, { isAdmin, pending: true }).startable)
  const upgrading = Boolean(source) && actions.state[`upgrade:${app.id}:${app.generation}`]?.phase === 'working'
  return (
    <div className="ad-pane__body">
      {attention === 'failed' && (
        <Callout tone="error" title={t('attention.failedTitle', { name: title })} testId="pane-failed">
          {restarts > 0 ? t('attention.failedRestarts', { count: restarts }) : t('attention.failedOnce')}{' '}
          {added.tools > 0 ? t('attention.toolsOff', { count: added.tools }) : added.page ? t('attention.pageOff') : ''}
        </Callout>
      )}
      {canUpgrade && (
        <Callout
          tone="warn"
          icon="arrow-up"
          title={t('preview.updateTitle', { version: update.version })}
          testId="pane-update"
          actions={<Button size="sm" busy={upgrading} disabled={upgrading || pending} onClick={() => upgrade.ask(source)} aria-label={t('updates.upgradeNamed', { name: title })}>{t('updates.upgrade')}</Button>}
        >
          {t('attention.updateBody', { current: source.version })}
        </Callout>
      )}
      {description && <p className="ad-pane__text">{description}</p>}
      <div className="ad-pane__acts">
        {app.mainPage && (
          <Button variant="primary" size="sm" icon="external-link" to={app.mainPage}>{t('apps.open')}</Button>
        )}
        {control.start && (
          <Button variant={app.mainPage ? 'secondary' : 'primary'} size="sm" icon="toggle-on" busy={working} disabled={locked} onClick={() => onStartStop(app, 'start')}>{t('apps.start')}</Button>
        )}
        {control.stop && (
          <Button variant="ghost" size="sm" icon="toggle-off" busy={working} disabled={locked} onClick={() => onStartStop(app, 'stop')}>{t('apps.stop')}</Button>
        )}
        <Button size="sm" variant="ghost" icon="pin" aria-pressed={pinned} onClick={onPin}>{pinned ? t('drawer.pinned') : t('drawer.pinLabel')}</Button>
      </div>
      {working && <p className="ad-card__note" role="status">{t('apps.working')}</p>}
      {pending
        ? <UnknownNotice addonId={app.id} actions={actions} className="ad-card__note" />
        : outcome && outcome.outcome && describeOutcome(outcome.outcome, t, outcome.error) && (
          <Callout tone="error" role="alert">{describeOutcome(outcome.outcome, t, outcome.error)}</Callout>
        )}
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
                {added.toolNames.length > 0 && <span className="ad-adds__ids"><span className="dk-mono">{added.toolNames.join(', ')}</span>{added.toolsMore > 0 ? ` ${t('apps.toolsMore', { count: added.toolsMore })}` : ''}</span>}
              </span>
            </li>
          )}
          {!added.page && added.tools === 0 && <li className="ad-quiet">{t('preview.addsNothing')}</li>}
        </ul>
        {!added.page && <p className="ad-quiet ad-pane__text">{t('apps.headless')}</p>}
        {links.length > 0 && (
          <div className="ad-pane__acts">
            {links.map(link => <Button key={link.path} variant="ghost" size="sm" to={link.path}>{link.label}</Button>)}
          </div>
        )}
      </section>
      <dl className="dk-kv ad-kv ad-pane__facts" data-testid="preview-facts">
        <dt>{t('preview.version')}</dt><dd>{app.version}</dd>
        {isAdmin && <><dt>{t('trust.library')}</dt><dd data-testid="preview-source">{source ? `${source.sourceId || '—'}${source.channel ? ` · ${source.channel}` : ''}` : t('preview.sourceUnknown')}</dd></>}
        <dt>{t('preview.id')}</dt><dd className="dk-mono">{app.id}</dd>
      </dl>
      {children}
      {onRemove && (
        <div className="ad-pane__danger">
          <Button variant="danger-ghost" size="sm" icon="trash" disabled={locked} onClick={onRemove} data-testid="pane-remove">{t('card.removeNamed', { name: title })}</Button>
        </div>
      )}
    </div>
  )
}
