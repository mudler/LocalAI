import { useTranslation } from 'react-i18next'
import { hostMemory, memoryFigure, fillStyle, percent } from './memory'
import Icon from '../Icon'

// eslint-disable-next-line no-unused-vars
function Bar({ pct, thin = false, label }) {
  return (
    <span
      className={`home-bar${thin ? ' home-bar--thin' : ''}`}
      role="img"
      aria-label={label}
      data-level={pct >= 90 ? 'warn' : undefined}
    >
      <span className="home-bar__fill" style={fillStyle(pct)} />
    </span>
  )
}

// eslint-disable-next-line no-unused-vars
function Figure({ used, total }) {
  const f = memoryFigure(used, total)
  if (!f) return null
  return <span className="home-fig">{f.used} <span>/ {f.total} {f.unit}</span></span>
}

// One line about memory that opens into the list of loaded models. It opens by
// itself while a model is being staged and after a failure, because memory and
// models are the message then.
//
// The bar is the memory in use on the device, drawn as one solid fill. The API
// reports no per-model figure, so the list carries the model and its engine and
// no size.
export default function HomeMemoryStrip({
  open, onOpenChange, models, resources, cluster, stagingOp, failedOp, onStop, onStopAll,
}) {
  const { t } = useTranslation('home')
  const count = models.length
  const host = hostMemory(resources)
  const clustered = !!cluster
  const body = clustered || count > 0 || host || stagingOp || failedOp

  let lead
  let text
  let mode = ''
  if (failedOp) {
    mode = 'error'
    lead = <Icon name="alert-circle" />
    text = <span><b>{failedOp.name || failedOp.id}</b> {t('strip.failed')}</span>
  } else if (stagingOp) {
    lead = <span className="home-spinner" aria-hidden="true" />
    text = (
      <span>
        <b>{t('strip.staging', { name: stagingOp.name || stagingOp.id })}</b>
        {stagingOp.nodeName ? ` ${t('strip.stagingTo', { node: stagingOp.nodeName })}` : ''}
      </span>
    )
  } else if (clustered) {
    lead = <span className="home-dot" aria-hidden="true" />
    text = (
      <span>
        <b data-testid="home-stat-loaded">{count}</b> {t('strip.modelsOn', { count })}{' '}
        <b data-testid="home-stat-nodes">{cluster.healthyCount}/{cluster.totalCount}</b> {t('strip.nodes', { count: cluster.totalCount })}
      </span>
    )
  } else {
    lead = <span className={`home-dot${count > 0 ? '' : ' home-dot--cold'}`} aria-hidden="true" />
    text = count > 0
      ? <span><b data-testid="home-stat-loaded">{count}</b> {t('strip.modelsLoaded', { count })}</span>
      : <span data-testid="home-stat-loaded-none">{t('strip.noneLoaded')}</span>
  }

  const memUsed = clustered ? cluster.usedMem : host?.used
  const memTotal = clustered ? cluster.totalMem : host?.total
  const memPct = clustered ? percent(cluster.usedMem, cluster.totalMem) : host?.pct
  const showMem = !failedOp && memTotal > 0

  return (
    <section className="home-strip" data-open={open ? 'true' : undefined} data-mode={mode || undefined} aria-label={t('strip.label')}>
      <button
        type="button"
        className="home-strip__head"
        aria-expanded={open}
        aria-controls="home-strip-body"
        onClick={() => onOpenChange(!open)}
        disabled={!body}
      >
        {lead}
        {text}
        {stagingOp && stagingOp.progress > 0 && !failedOp && (
          <>
            <Bar pct={stagingOp.progress} label={t('strip.progress', { name: stagingOp.name || stagingOp.id })} />
            <span className="home-fig">{Math.round(stagingOp.progress)}%</span>
          </>
        )}
        {showMem && !(stagingOp && stagingOp.progress > 0) && (
          <>
            {!clustered && <Bar pct={memPct} label={t('strip.memoryLabel')} />}
            <Figure used={memUsed} total={memTotal} />
          </>
        )}
        {!clustered && host && !failedOp && !stagingOp && (
          <span className="home-device">
            {host.isGpu ? (host.gpuCount > 1 ? t('strip.gpus', { count: host.gpuCount }) : (host.device || t('resourceGpu'))) : t('resourceRam')}
          </span>
        )}
        {clustered && !failedOp && !stagingOp && cluster.nodes.length > 0 && (
          <span className="home-mini">
            {cluster.nodes.slice(0, 3).map(n => (
              <span key={n.id} title={n.name}>
                <span className="home-mini__name">{n.name}</span>
                <Bar thin pct={percent(n.used, n.total)} label={`${n.name}: ${t('strip.memoryLabel')}`} />
              </span>
            ))}
          </span>
        )}
        <Icon name="chevron-down" className="home-strip__caret" />
      </button>

      {open && body && (
        <div className="home-strip__body" id="home-strip-body">
          {clustered && cluster.nodes.length > 0 && (
            <ul className="home-nodes" aria-label={t('strip.nodesLabel')}>
              {cluster.nodes.map(n => (
                <li key={n.id}>
                  <span className={`home-dot${n.healthy ? '' : ' home-dot--cold'}`} aria-hidden="true" />
                  <span className="home-nodes__name">{n.name}</span>
                  <Bar thin pct={percent(n.used, n.total)} label={`${n.name}: ${t('strip.memoryLabel')}`} />
                  <Figure used={n.used} total={n.total} />
                </li>
              ))}
            </ul>
          )}

          {count > 0 ? (
            <ul className="home-loaded" aria-label={t('loadedModels.heading')}>
              {[...models].sort((a, b) => a.id.localeCompare(b.id)).map(m => (
                <li key={m.id} className="home-loaded__row" data-testid="home-loaded-row">
                  <span className="home-dot" aria-hidden="true" />
                  <span className="home-loaded__name">
                    <code>{m.id}</code>
                    {/* The engine is shown when the model has a config to read it
                        from, and left out otherwise. */}
                    {m.backend && <small>{m.backend}</small>}
                  </span>
                  <button
                    type="button"
                    className="home-loaded__stop"
                    onClick={() => onStop(m.id)}
                    title={t('loadedModels.stop')}
                    aria-label={`${t('loadedModels.stop')}: ${m.id}`}
                  >
                    <Icon name="close" />
                  </button>
                </li>
              ))}
            </ul>
          ) : (
            <p className="home-loaded__empty">{t('statusLine.noModelsLoaded')}</p>
          )}

          {!clustered && host?.isGpu && host.ram && (
            <div className="home-ram">
              <span>{t('resourceRam')}</span>
              <Bar thin pct={percent(host.ram.used, host.ram.total)} label={t('strip.ramLabel')} />
              <Figure used={host.ram.used} total={host.ram.total} />
            </div>
          )}

          {count > 1 && (
            <div className="home-strip__foot">
              <button type="button" className="home-ghost" onClick={onStopAll}>{t('loadedModels.stopAll')}</button>
            </div>
          )}
        </div>
      )}
    </section>
  )
}
