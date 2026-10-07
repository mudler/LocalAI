import { useCleanupFacts } from '../../../hooks/useCleanupFacts'
// eslint-disable-next-line no-unused-vars
import NodeDistributionChip from '../../NodeDistributionChip'
import Icon from '../../Icon'

const MISSING = ['requests', 'ttft', 'loads', 'changes']

// Usage and history. The API keeps no per-model request count, no time to first
// token and no record of loads or configuration changes, so there is nothing to
// chart and the tab says so, naming what will appear and what is missing. What
// the server does know about the model today (its state, who uses it) is listed
// as facts, so the tab is not empty of the truth.
export default function UsageTab({ view }) {
  const { t, id, profile, running } = view
  const facts = useCleanupFacts(true)
  const refs = facts.references.get(id) || []
  const kinds = ['agent', 'task', 'chain', 'alias']
    .map(kind => ({ kind, count: refs.filter(ref => ref.kind === kind).length }))
    .filter(item => item.count > 0)
  const nodes = Array.isArray(profile?.loaded_on) ? profile.loaded_on : []
  return (
    <div className="modelpage-usage" data-testid="model-page-usage">
      <section aria-labelledby="modelpage-usage-h">
        <div className="modelpage-section-head">
          <h2 className="modelpage-section-title" id="modelpage-usage-h">{t('page.usage.title')}</h2>
        </div>
        <div className="dk-empty modelpage-usage__empty" data-testid="usage-empty">
          <div className="dk-empty-icon"><Icon name="chart-bar" /></div>
          <h3 className="dk-empty-title">{t('page.usage.emptyTitle')}</h3>
          <p className="dk-empty-text">{t('page.usage.emptyText')}</p>
        </div>
        <ul className="modelpage-usage__missing" aria-label={t('page.usage.missingLabel')} data-testid="usage-missing">
          {MISSING.map(key => (
            <li key={key}>
              <span>
                <strong>{t(`page.usage.missing.${key}.name`)}</strong>
                <span className="dk-hint">{t(`page.usage.missing.${key}.why`)}</span>
              </span>
              <span className="dk-badge">{t('page.usage.notRecorded')}</span>
            </li>
          ))}
        </ul>
      </section>

      <section aria-labelledby="modelpage-known-h">
        <div className="modelpage-section-head">
          <h2 className="modelpage-section-title" id="modelpage-known-h">{t('page.usage.known')}</h2>
        </div>
        <dl className="dk-kv modelpage-facts" data-testid="usage-known">
          <dt>{t('lifecycle.detail.state')}</dt>
          <dd>{profile.disabled ? t('lifecycle.states.disabled') : running ? t('lifecycle.states.running') : t('lifecycle.states.idle')}</dd>
          {nodes.length > 0 && (
            <>
              <dt>{t('lifecycle.detail.distributed')}</dt>
              <dd><NodeDistributionChip nodes={nodes} context="models" compactThreshold={20} /></dd>
            </>
          )}
          <dt>{t('page.usedBy.title')}</dt>
          <dd data-testid="usage-usedby">
            {!facts.loaded
              ? t('page.usedBy.loading')
              : kinds.length === 0
                ? (facts.verified ? t('page.usedBy.none') : t('page.usedBy.unknown'))
                : kinds.map(item => t(`page.usage.count.${item.kind}`, { count: item.count })).join(', ')}
          </dd>
        </dl>
      </section>
    </div>
  )
}
