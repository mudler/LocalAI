// Link and PageHeader are used in JSX only, which eslint cannot see.
// eslint-disable-next-line no-unused-vars
import { Link } from 'react-router-dom'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import PageHeader from '../components/PageHeader'
import Icon from '../components/Icon'
import { buildHub, isTabVisible, visibleItems } from '../components/hub/hubConfig'
import { useHubAuth } from '../components/hub/useHubAuth'
import { fineTuneApi, quantizationApi, resourcesApi } from '../utils/api'
import { formatBytes } from '../utils/format'
import { FT_STAGES, QZ_STAGES, jobToSurface, machineFacts, toolStatus } from '../utils/tools'
import { preloadRoute } from '../router'
import '../components/tools/tools.css'

// The tools in three groups. A tool the viewer cannot use is left out, so a
// group with nothing in it is left out too.
const GROUPS = [
  { id: 'make', tools: ['import', 'quantize', 'fine-tune'] },
  { id: 'recognise', tools: ['voices', 'faces'] },
  { id: 'automate', tools: ['agents', 'skills', 'memory', 'jobs'] },
]
const EXPERIMENTAL = new Set(['quantize', 'fine-tune'])
const CHECKED = new Set(['import', 'quantize', 'fine-tune'])

function sized(vals) {
  const out = { ...(vals || {}) }
  for (const key of ['free', 'total']) if (typeof out[key] === 'number') out[key] = formatBytes(out[key])
  return out
}

// The Build landing: what each tool is for and what it needs from this machine.
// The needs are read from the machine's own figures and the installed backends,
// never guessed. A tool that cannot run says why and what enables it. One line
// above the list shows a job that is running or the newest job when it failed.
export default function BuildOverview() {
  const { t } = useTranslation('tools')
  const { t: tn } = useTranslation('nav')
  const auth = useHubAuth()
  const visible = useMemo(
    () => buildHub.tabs.filter(tab => tab.id !== 'overview' && isTabVisible(tab, auth)),
    [auth],
  )
  const ids = useMemo(() => new Set(visible.map(tab => tab.id)), [visible])
  const hasFineTune = ids.has('fine-tune')
  const hasQuantize = ids.has('quantize')

  const [facts, setFacts] = useState(null)
  const [backends, setBackends] = useState({ fineTune: null, quantize: null })
  const [jobs, setJobs] = useState({ fineTune: [], quantize: [] })

  useEffect(() => {
    if (!auth.isAdmin) return undefined
    let alive = true
    resourcesApi.get().then(r => { if (alive) setFacts(machineFacts(r)) }).catch(() => {})
    return () => { alive = false }
  }, [auth.isAdmin])

  useEffect(() => {
    let alive = true
    if (hasFineTune) {
      fineTuneApi.listBackends().then(b => { if (alive) setBackends(prev => ({ ...prev, fineTune: Array.isArray(b) ? b : [] })) }).catch(() => {})
      fineTuneApi.listJobs().then(j => { if (alive) setJobs(prev => ({ ...prev, fineTune: Array.isArray(j) ? j : [] })) }).catch(() => {})
    }
    if (hasQuantize) {
      quantizationApi.listBackends().then(b => { if (alive) setBackends(prev => ({ ...prev, quantize: Array.isArray(b) ? b : [] })) }).catch(() => {})
      quantizationApi.listJobs().then(j => { if (alive) setJobs(prev => ({ ...prev, quantize: Array.isArray(j) ? j : [] })) }).catch(() => {})
    }
    return () => { alive = false }
  }, [hasFineTune, hasQuantize])

  const now = useMemo(() => {
    const ft = hasFineTune ? jobToSurface(jobs.fineTune, FT_STAGES) : null
    const qz = hasQuantize ? jobToSurface(jobs.quantize, QZ_STAGES) : null
    const picks = [ft && { ...ft, tool: 'fine-tune', path: '/app/fine-tune' }, qz && { ...qz, tool: 'quantize', path: '/app/quantize' }].filter(Boolean)
    return picks.find(p => p.kind === 'running') || picks[0] || null
  }, [jobs, hasFineTune, hasQuantize])

  const byId = Object.fromEntries(visible.map(tab => [tab.id, tab]))
  const groups = GROUPS.map(g => ({ ...g, list: g.tools.map(id => byId[id]).filter(Boolean) })).filter(g => g.list.length > 0)
  const makeIds = (groups.find(g => g.id === 'make')?.list || []).map(tab => tab.id)

  const quantBackend = Array.isArray(backends.quantize) ? backends.quantize.map(b => b.name) : []
  const trainBackend = Array.isArray(backends.fineTune) ? backends.fineTune.map(b => b.name) : []
  const installedBackends = [...new Set([...quantBackend, ...trainBackend])]

  return (
    <div className="page page--medium bt-page" data-testid="build-landing">
      <PageHeader title={tn('sections.build')} supporting={t('landing.lede')} />

      {visible.length === 0 ? (
        <p className="bt-empty" data-testid="build-empty">{tn('hub.overviewEmpty')}</p>
      ) : (
        <>
          {facts && (
            <section className="bt-machine dk-card dk-card--tight" aria-label={t('landing.machine')} data-testid="build-machine">
              <span className="dk-eyebrow">{t('landing.machine')}</span>
              <dl className="bt-machine__facts">
                <div>
                  <dt><Icon name="cpu" /> {t('landing.gpu')}</dt>
                  <dd>
                    {facts.cluster
                      ? t('landing.cluster', sized({ total: facts.cluster.total, node: facts.cluster.node || t('landing.clusterNode') }))
                      : facts.hasGpu
                        ? <><b className="dk-mono">{facts.gpuName}</b> {t('landing.gpuFree', sized({ free: facts.vramFree, total: facts.vramTotal }))}</>
                        : t('landing.noGpu')}
                  </dd>
                </div>
                {facts.ramTotal != null && (
                  <div>
                    <dt><Icon name="memory" /> {t('landing.ram')}</dt>
                    <dd><b className="dk-mono">{formatBytes(facts.ramTotal)}</b> {facts.ramFree != null && t('landing.ramFree', sized({ free: facts.ramFree }))}</dd>
                  </div>
                )}
                {facts.diskFree != null && (
                  <div>
                    <dt><Icon name="hard-drive" /> {t('landing.disk')}</dt>
                    <dd><b className="dk-mono">{formatBytes(facts.diskFree)}</b> {t('landing.diskFree')}</dd>
                  </div>
                )}
              </dl>
              {(hasFineTune || hasQuantize) && (
                <p className="bt-machine__backends">
                  <Icon name="plug" /> {t('landing.backends')}{' '}
                  {installedBackends.length > 0
                    ? installedBackends.map(name => <span key={name} className="dk-chip dk-chip--sm dk-mono">{name}</span>)
                    : <span className="dk-muted">{t('landing.noBackends')}</span>}
                </p>
              )}
            </section>
          )}

          {now && (
            <section className="bt-now" role="status" data-testid="build-now" data-kind={now.kind}>
              <span className="bt-now__mark"><Icon name={now.kind === 'running' ? 'spinner' : 'alert-circle'} spin={now.kind === 'running'} /></span>
              <div className="bt-now__main">
                <p className="bt-now__title">
                  {t(`landing.now.${now.kind}.${now.tool === 'fine-tune' ? 'fineTune' : 'quantize'}`, {
                    model: now.job.model,
                    type: now.job.quantization_type || '',
                  })}
                </p>
                {now.job.message && <p className="bt-now__text">{now.job.message}</p>}
              </div>
              <Link className="dk-btn dk-btn--secondary dk-btn--sm" to={now.path}>{t('landing.open')}</Link>
            </section>
          )}

          <ul className="bt-groups" aria-label={tn('hub.toolsLabel')}>
            {groups.map(group => (
              <li key={group.id} className="bt-group" data-group={group.id}>
                <header className="bt-group__head">
                  <h2 className="bt-h2">{t(`landing.groups.${group.id}.title`)}</h2>
                  <p className="dk-hint">{t(`landing.groups.${group.id}.text`)}</p>
                </header>
                {group.id === 'make' && makeIds.length > 1 && (
                  <div className="bt-flow">
                    <ol className="bt-flow__list" aria-label={t('landing.flow')}>
                      {[...makeIds.map(id => ({ id, icon: byId[id].icon, label: tn(byId[id].labelKey) })), { id: 'chat', icon: 'chat', label: tn('items.chat') }].map((step, i) => (
                        <li key={step.id} className="bt-flow__item">
                          {i > 0 && <Icon name="arrow-right" aria-hidden="true" />}
                          <span className="dk-chip dk-chip--sm"><Icon name={step.icon} /> {step.label}</span>
                        </li>
                      ))}
                    </ol>
                    <span className="dk-hint">{t('landing.flowNote')}</span>
                  </div>
                )}
                <ul className="bt-tools">
                  {group.list.map(tool => {
                    const path = visibleItems(tool, auth)[0].path
                    const status = CHECKED.has(tool.id) ? toolStatus(tool.id, { facts, backends }) : null
                    const sentenceKey = `landing.sentence.${tool.id}`
                    return (
                      <li key={tool.id} className="bt-tool" data-tool={tool.id} data-state={status?.state}>
                        <span className="bt-tool__icon"><Icon name={tool.icon} aria-hidden="true" /></span>
                        <div className="bt-tool__main">
                          <h3 className="bt-tool__title">
                            {tn(tool.labelKey)}
                            {EXPERIMENTAL.has(tool.id) && <span className="dk-badge dk-badge--warn">{t('landing.experimental')}</span>}
                          </h3>
                          <p className="bt-tool__text">{CHECKED.has(tool.id) ? t(sentenceKey) : tn(tool.descriptionKey)}</p>
                          {status && status.needs.length > 0 && (
                            <ul className="bt-needs" aria-label={t('landing.needs')}>
                              {status.needs.map(need => (
                                <li key={need.key} data-tone={need.tone}>
                                  <Icon name={need.tone === 'ok' ? 'check-circle' : need.tone === 'warn' ? 'warning' : 'info'} aria-hidden="true" />
                                  <span>{t(`landing.line.${need.key}`, sized(need.vals))}</span>
                                </li>
                              ))}
                            </ul>
                          )}
                          {status?.state === 'needs-backend' && (
                            <p className="bt-tool__why">
                              {auth.isAdmin ? t('landing.enable.admin') : t('landing.enable.user')}
                            </p>
                          )}
                        </div>
                        <div className="bt-tool__end">
                          {status && (
                            <span className={`dk-badge ${status.state === 'ready' ? 'dk-badge--ok' : 'dk-badge--warn'}`}>
                              {status.state === 'ready' ? t('landing.ready') : t('landing.needsBackend')}
                            </span>
                          )}
                          {status?.state === 'needs-backend' && auth.isAdmin && (
                            <Link className="dk-btn dk-btn--secondary dk-btn--sm" to="/app/backends">{t('landing.install')}</Link>
                          )}
                          <Link
                            className={`dk-btn dk-btn--${status?.state === 'needs-backend' ? 'ghost' : 'secondary'} dk-btn--sm`}
                            to={path}
                            aria-label={t('landing.openTool', { tool: tn(tool.labelKey) })}
                            onMouseEnter={() => preloadRoute(path)}
                            onFocus={() => preloadRoute(path)}
                          >
                            {t('landing.open')} <Icon name="arrow-right" aria-hidden="true" />
                          </Link>
                        </div>
                      </li>
                    )
                  })}
                </ul>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  )
}
