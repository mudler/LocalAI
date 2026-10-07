// eslint-disable-next-line no-unused-vars
import { Link } from 'react-router-dom'
import { useCleanupFacts } from '../../../hooks/useCleanupFacts'
import { renderMarkdown, stripMarkdown } from '../../../utils/markdown'
import { safeHref } from '../../../utils/url'
import { gbLabel } from '../../../utils/modelLedger'
import { contextLabel } from '../../../utils/placement'
import Icon from '../../Icon'
// eslint-disable-next-line no-unused-vars
import MemoryBar from '../MemoryBar'
import { fitWords, memorySegments } from './fitFacts'

// The answer to "should I use this": three plain statements in one strip. Fit on
// this machine, what the model does, and what happens next (install it, or its
// state when it is already here).
// eslint-disable-next-line no-unused-vars
function AnswerStrip({ view }) {
  const { t, entry, installed, running, profile, useCases, installing, progress, failedOp, estimateState, leaves, sizeBytes } = view
  const words = fitWords(view)
  const tags = (entry?.tags || []).filter(tag => !['gguf'].includes(String(tag).toLowerCase())).slice(0, 4)
  return (
    <section className="modelpage-answer dk-card" aria-label={t('page.answer.label')} data-testid="model-page-answer">
      <div className="modelpage-answer__cell" data-testid="answer-fit">
        <span className="dk-eyebrow">{t('page.answer.fit')}</span>
        {words ? (
          <>
            <strong className="modelpage-answer__head" data-tone={words.tone}>
              <span className={`dk-dot dk-dot--${words.tone === 'ok' ? 'ok' : words.tone === 'warn' ? 'warn' : 'error'}`} /> {words.title}
            </strong>
            <span className="modelpage-answer__text">{words.text}</span>
          </>
        ) : (
          <span className="modelpage-answer__text">
            {estimateState.status === 'loading' ? t('page.answer.fitLoading') : t('page.answer.fitNone')}
          </span>
        )}
      </div>
      <div className="modelpage-answer__cell" data-testid="answer-does">
        <span className="dk-eyebrow">{t('page.answer.does')}</span>
        <div className="modelpage-answer__chips">
          {(installed && useCases.length > 0 ? useCases.map(u => t(`lifecycle.open.${u.labelKey}`)) : tags).map(label => (
            <span key={label} className="dk-chip dk-chip--sm modelpage-chip">{label}</span>
          ))}
        </div>
        {installed && useCases.some(u => u.route) && (
          <span className="modelpage-answer__text">
            {t('page.answer.opensIn', { useCases: useCases.filter(u => u.route).map(u => t(`lifecycle.open.${u.labelKey}`)).join(', ') })}
          </span>
        )}
        {!installed && tags.length > 0 && <span className="modelpage-answer__text">{t('page.answer.opensAfter')}</span>}
      </div>
      <div className="modelpage-answer__cell" data-testid="answer-state">
        <span className="dk-eyebrow">{installed ? t('page.answer.state') : t('page.answer.install')}</span>
        {installing ? (
          <>
            <strong className="modelpage-answer__head">{progress > 0 ? t('page.answer.installingPct', { percent: progress }) : t('page.answer.installing')}</strong>
            <span className="modelpage-answer__text">{t('page.answer.installingNote')}</span>
          </>
        ) : failedOp ? (
          <>
            <strong className="modelpage-answer__head" data-tone="error">{t('page.answer.failed')}</strong>
            <span className="modelpage-answer__text">{failedOp.error}</span>
          </>
        ) : installed ? (
          <>
            <strong className="modelpage-answer__head">
              <span className={`dk-dot${running ? ' dk-dot--ok' : ''}`} /> {profile.disabled ? t('lifecycle.states.disabled') : running ? t('lifecycle.states.running') : t('lifecycle.states.idle')}
            </strong>
            <span className="modelpage-answer__text">{running ? t('page.answer.runningNote') : profile.disabled ? t('page.answer.disabledNote') : t('page.answer.idleNote')}</span>
          </>
        ) : (
          <>
            <strong className="modelpage-answer__head">{sizeBytes ? gbLabel(sizeBytes) : t('page.answer.notInstalled')}</strong>
            <span className="modelpage-answer__text">
              {leaves !== null && leaves !== undefined
                ? (leaves < 0 ? t('disk.notEnough', { amount: gbLabel(-leaves), size: gbLabel(sizeBytes) }) : t('page.answer.leaves', { amount: gbLabel(leaves) }))
                : t('page.answer.notInstalledNote')}
            </span>
          </>
        )}
      </div>
    </section>
  )
}

// eslint-disable-next-line no-unused-vars
function GlanceBar({ view }) {
  const { t, fit, budget, estimateState, contextSize } = view
  if (!fit) return null
  const segments = memorySegments(estimateState.data, contextSize, fit.need, t)
  const pool = budget.hasGpu ? t('page.fit.poolGpu', { memory: gbLabel(fit.total) }) : t('page.fit.poolRam', { memory: gbLabel(fit.total) })
  const left = fit.limit - fit.need
  return (
    <section className="modelpage-glance" aria-labelledby="modelpage-glance-h" data-testid="model-page-glance">
      <div className="modelpage-section-head">
        <h2 className="modelpage-section-title" id="modelpage-glance-h">{t('page.overview.glance')}</h2>
        <button type="button" className="dk-link modelpage-glance__link" onClick={() => view.goTab('fit')}>
          {t('page.tabs.fit')} <Icon name="arrow-right" />
        </button>
      </div>
      <div className="modelpage-glance__head">
        <span className="modelpage-glance__pool">{pool}</span>
        <span className={`modelpage-glance__free${left < 0 ? ' modelpage-glance__free--over' : ''}`}>
          {left < 0 ? t('placement.bar.over', { amount: gbLabel(-left) }) : t('placement.bar.freeAfter', { amount: gbLabel(left) })}
        </span>
      </div>
      <MemoryBar
        capacity={fit.limit}
        segments={segments}
        ariaLabel={t('page.fit.barAria', { pool, need: gbLabel(fit.need), limit: gbLabel(fit.limit) })}
        testId="glance-bar"
      />
    </section>
  )
}

// eslint-disable-next-line no-unused-vars
function UsedBy({ view }) {
  const { t, id } = view
  const facts = useCleanupFacts(true)
  const refs = facts.references.get(id) || []
  return (
    <section className="modelpage-usedby" aria-labelledby="modelpage-usedby-h" data-testid="model-page-usedby">
      <h2 className="modelpage-section-title" id="modelpage-usedby-h">{t('page.usedBy.title')}</h2>
      {!facts.loaded ? (
        <span className="dk-skeleton dk-skeleton--line modelpage-usedby__loading" role="status" aria-label={t('page.usedBy.loading')} />
      ) : refs.length === 0 ? (
        <p className="dk-hint">{facts.verified ? t('page.usedBy.none') : t('page.usedBy.unknown')}</p>
      ) : (
        <ul className="modelpage-usedby__list">
          {refs.map(ref => (
            <li key={`${ref.kind}:${ref.name}`}>
              <span className="modelpage-usedby__name">
                {ref.kind === 'agent'
                  ? <Link className="dk-link" to={`/app/agents/${encodeURIComponent(ref.name)}/edit`}>{ref.name}</Link>
                  : ref.kind === 'chain'
                    ? <Link className="dk-link" to="/app/failover">{ref.name}</Link>
                    : ref.name}
              </span>
              <span className="dk-eyebrow">{t(`page.usedBy.kind.${ref.kind}`)}</span>
            </li>
          ))}
        </ul>
      )}
      {facts.loaded && !facts.verified && refs.length > 0 && <p className="dk-hint">{t('page.usedBy.partial')}</p>}
    </section>
  )
}

export default function OverviewTab({ view }) {
  const { t, entry, profile, installed, running, backend, license, estimateState, useCases, lede } = view
  // A one-line description is already the line under the title; saying it again
  // as a paragraph adds nothing. Longer prose, and anything with markup, stays.
  const description = entry?.description && !(lede && !/[\n#*_`\[]/.test(entry.description) && stripMarkdown(entry.description).trim() === lede)
    ? entry.description
    : null
  const maxContext = estimateState.data?.modelMaxContext
  const tags = Array.isArray(entry?.tags) ? entry.tags : []
  const urls = Array.isArray(entry?.urls) ? entry.urls : []
  return (
    <div className="modelpage-overview">
      <AnswerStrip view={view} />
      <div className="modelpage-columns">
        <section className="modelpage-about" aria-labelledby="modelpage-about-h">
          <h2 className="modelpage-section-title" id="modelpage-about-h">{t('page.overview.about')}</h2>
          {description ? (
            <div className="markdown-body modelpage-prose" dangerouslySetInnerHTML={{ __html: renderMarkdown(description) }} />
          ) : !lede && (
            <p className="dk-hint">{t('lifecycle.detail.noDescription')}</p>
          )}
          <dl className="dk-kv modelpage-facts">
            {backend && (<><dt>{t('detail.backend')}</dt><dd><span className="dk-chip dk-chip--sm modelpage-chip dk-mono">{backend}</span></dd></>)}
            {entry?.gallery && (<><dt>{t('detail.gallery')}</dt><dd>{typeof entry.gallery === 'string' ? entry.gallery : entry.gallery.name}</dd></>)}
            {license && (<><dt>{t('detail.license')}</dt><dd>{license}</dd></>)}
            {maxContext > 0 && (<><dt>{t('page.overview.maxContext')}</dt><dd>{t('page.overview.tokens', { size: contextLabel(maxContext) })}</dd></>)}
            {tags.length > 0 && (
              <>
                <dt>{t('detail.tags')}</dt>
                <dd><div className="modelpage-tags">{tags.map(tag => <span key={tag} className="dk-chip dk-chip--sm modelpage-chip">{tag}</span>)}</div></dd>
              </>
            )}
            {urls.length > 0 && (
              <>
                <dt>{t('detail.links')}</dt>
                <dd>
                  <div className="modelpage-links">
                    {urls.map(url => (
                      <a key={url} className="dk-link" href={safeHref(url)} target="_blank" rel="noopener noreferrer">
                        <Icon name="external-link" className="icon-before" />{url}
                      </a>
                    ))}
                  </div>
                </dd>
              </>
            )}
            {entry?.trustRemoteCode && (<><dt>{t('detail.warning')}</dt><dd><span className="dk-badge dk-badge--error"><Icon name="alert-circle" /> {t('detail.requiresTrustRemoteCode')}</span></dd></>)}
          </dl>
        </section>

        <aside className="modelpage-aside">
          <GlanceBar view={view} />
          {installed && (
            <section className="modelpage-status" aria-labelledby="modelpage-status-h" data-testid="model-page-status">
              <h2 className="modelpage-section-title" id="modelpage-status-h">{t('page.overview.status')}</h2>
              <dl className="dk-kv modelpage-facts">
                <dt>{t('lifecycle.detail.state')}</dt>
                <dd>{profile.disabled ? t('lifecycle.states.disabled') : running ? t('lifecycle.states.running') : t('lifecycle.states.idle')}</dd>
                <dt>{t('lifecycle.detail.backend')}</dt>
                <dd className="dk-mono">{profile.backend || t('lifecycle.detail.auto')}</dd>
                {profile.pinned && (<><dt>{t('lifecycle.detail.pinned')}</dt><dd>{t('lifecycle.detail.yes')}</dd></>)}
                {profile.source && (<><dt>{t('lifecycle.detail.source')}</dt><dd>{profile.source}</dd></>)}
                {useCases.length > 0 && (
                  <>
                    <dt>{t('lifecycle.open.title')}</dt>
                    <dd>
                      <div className="modelpage-tags">
                        {useCases.map(u => u.route
                          ? <Link key={u.cap} className="dk-chip dk-chip--sm modelpage-chip" to={u.route(view.id)}>{t(`lifecycle.open.${u.labelKey}`)}</Link>
                          : <span key={u.cap} className="dk-chip dk-chip--sm modelpage-chip">{t(`lifecycle.open.${u.labelKey}`)}</span>)}
                      </div>
                    </dd>
                  </>
                )}
              </dl>
            </section>
          )}
          {installed && <UsedBy view={view} />}
        </aside>
      </div>
    </div>
  )
}
