import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { fetchUrl } from '../utils/api'
import { useAuth } from '../context/AuthContext'
import Icon from './Icon'
// eslint-disable-next-line no-unused-vars
import Sheet from '../../../../talk/ui/src/components/talk/Sheet'
import {
  CANDIDATES, OPTIONAL, PARTS, choosePipeline, createSetupApi, formatBytes, itemBytes, pipelineConfig, planSetup, runSetup, sizeUnknown, totalBytes,
} from '../lib/setup'

// The first pick of a part: a candidate that is installed already, so the
// setup skips it, else the first candidate.
function initialPicks(groups) {
  const out = {}
  for (const part of PARTS) {
    const installed = groups[part] || []
    out[part] = CANDIDATES[part].find((c) => c && installed.includes(c)) || CANDIDATES[part][0]
  }
  return out
}

// Installs what a talking head needs and writes a pipeline model, then
// hands the names to the page. Each part can be changed before the install;
// installed parts are skipped, so running it again only fills the gaps.
export default function SetupSheet({ groups, installedNames, pipelines, onDone, onClose }) {
  const { t } = useTranslation('head')
  const { isAdmin } = useAuth()
  const api = useMemo(() => createSetupApi((path, init) => fetchUrl(path, init)), [])
  const installed = useMemo(() => new Set(installedNames), [installedNames])
  const [picks, setPicks] = useState(() => initialPicks(groups))
  const [plan, setPlan] = useState(null) // null while it loads
  const [planError, setPlanError] = useState(null)
  const [variants, setVariants] = useState({}) // part -> chosen build
  const [agreed, setAgreed] = useState(false)
  const [phase, setPhase] = useState('plan') // plan | running | done | failed | cancelled
  const [progress, setProgress] = useState({}) // part or 'pipeline' -> { state, progress, error, current, total }
  const abortRef = useRef(null)

  useEffect(() => {
    if (!isAdmin) return undefined
    let stale = false
    setPlan(null)
    setPlanError(null)
    planSetup(api, picks, installed)
      .then((items) => { if (!stale) setPlan(items) })
      .catch((err) => { if (!stale) setPlanError(err) })
    return () => { stale = true }
  }, [api, picks, installed, isAdmin])

  useEffect(() => () => abortRef.current?.abort(), [])

  const target = choosePipeline(pipelines, installedNames, picks)
  const items = (plan || []).map((i) => (variants[i.part] ? { ...i, variant: variants[i.part] } : i))
  const toInstall = items.filter((i) => !i.installed && !i.error)
  const blocked = items.some((i) => i.error)
  const avatar = items.find((i) => i.part === 'avatar')
  const needsLicence = Boolean(avatar && !avatar.installed)
  const nothingToDo = plan && toInstall.length === 0 && target.exists
  const running = phase === 'running'

  const start = async () => {
    const controller = new AbortController()
    abortRef.current = controller
    setPhase('running')
    setProgress((p) => Object.fromEntries(Object.entries(p).filter(([, v]) => v.state === 'done')))
    try {
      await runSetup({
        api,
        items: items.map((i) => ({ ...i, done: progress[i.part]?.state === 'done' })),
        pipeline: pipelineConfig(target.name, picks),
        pipelineExists: target.exists,
        signal: controller.signal,
        onItem: (key, patch) => setProgress((p) => ({ ...p, [key]: { ...p[key], ...patch } })),
      })
      setPhase('done')
      onDone({ ...picks, pipeline: target.name })
    } catch (err) {
      setPhase(err.name === 'AbortError' ? 'cancelled' : 'failed')
      if (err.forbidden) setPlanError(err)
    } finally {
      abortRef.current = null
    }
  }

  const options = (part) => {
    const list = [...CANDIDATES[part].filter(Boolean), ...(groups[part] || [])]
    if (picks[part] && !list.includes(picks[part])) list.unshift(picks[part])
    return [...new Set(list)]
  }

  const row = (key, name, extra) => {
    const st = progress[key] || {}
    const item = items.find((i) => i.part === key)
    const state = st.state || (item?.installed ? 'skipped' : item?.error ? 'error' : 'waiting')
    return (
      <li key={key} className="head-setup__item" data-part={key} data-state={state} data-testid={`head-setup-${key}`}>
        <div className="head-setup__line">
          <Icon name={{ done: 'check-circle', skipped: 'check-circle', installing: 'spinner', failed: 'alert-circle', error: 'alert-circle', cancelled: 'stop' }[state] || 'circle'} spin={state === 'installing'} />
          <span className="head-setup__part">{t(`setup.part.${key}`)}</span>
          <span className="head-setup__name">{name}</span>
          <span className="head-setup__state">
            {state === 'installing' && st.progress ? `${st.progress}%` : t(`setup.state.${state}`)}
          </span>
        </div>
        {state === 'installing' && <progress className="head-setup__bar" max="100" value={st.progress || 0} aria-label={t('setup.progress', { name })} />}
        {st.error && <p className="head-setup__error" role="alert">{st.error}</p>}
        {extra}
      </li>
    )
  }

  if (!isAdmin) {
    return (
      <Sheet id="head-setup" title={t('setup.title')} desc={t('setup.desc')} closeLabel={t('settings.close')} onClose={onClose} testId="head-setup">
        <section className="talk-sec">
          <div className="talk-alert" role="alert" data-testid="head-setup-forbidden">
            <Icon name="lock" />
            <div>
              <h3>{t('setup.adminTitle')}</h3>
              <p>{t('setup.adminBody')}</p>
            </div>
          </div>
          <ul className="head-missing">
            {PARTS.map((p) => CANDIDATES[p][0] && <li key={p}>{t(`setup.part.${p}`)}: <code>{CANDIDATES[p][0]}</code></li>)}
          </ul>
        </section>
      </Sheet>
    )
  }

  return (
    <Sheet id="head-setup" title={t('setup.title')} desc={t('setup.desc')} closeLabel={t('settings.close')} onClose={() => { if (!running) onClose() }} testId="head-setup">
      <section className="talk-sec">
        <h3>{t('setup.models')}</h3>
        {PARTS.map((part) => (
          <div className="talk-field" key={part}>
            <label htmlFor={`head-setup-pick-${part}`}>{t(`setup.part.${part}`)}</label>
            <select
              id={`head-setup-pick-${part}`}
              className="talk-in"
              value={picks[part]}
              disabled={running}
              onChange={(e) => { const v = e.target.value; setPicks((p) => ({ ...p, [part]: v })); setVariants((x) => ({ ...x, [part]: '' })); setPhase('plan'); setProgress({}) }}
            >
              {OPTIONAL.has(part) && <option value="">{t('setup.none')}</option>}
              {options(part).map((m) => <option key={m} value={m}>{(groups[part] || []).includes(m) || installed.has(m) ? t('setup.installedOption', { name: m }) : m}</option>)}
            </select>
          </div>
        ))}
        <p className="talk-help">{t('setup.pipelineNote', { name: target.name })}</p>
      </section>

      <section className="talk-sec" aria-live="polite">
        <h3>{t('setup.plan')}</h3>
        {planError ? (
          <p className="head-setup__error" role="alert" data-testid="head-setup-error">{planError.forbidden ? t('setup.forbidden') : t('setup.lookupFailed', { message: planError.message })}</p>
        ) : !plan ? (
          <p className="talk-help"><Icon name="spinner" spin /> {t('setup.checking')}</p>
        ) : (
          <>
            <ul className="head-setup" data-testid="head-setup-plan">
              {items.map((i) => row(i.part, i.name, (
                <>
                  {!i.installed && !i.error && <span className="head-setup__size">{formatBytes(itemBytes(i)) || t('setup.sizeUnknown')}{i.license ? ` · ${i.license}` : ''}</span>}
                  {i.error && <p className="head-setup__error">{t(`setup.errors.${i.error}`, { name: i.name, message: i.message || '' })}</p>}
                  {i.variants?.length > 0 && !i.installed && (
                    <div className="talk-field">
                      <label htmlFor={`head-setup-variant-${i.part}`}>{t('setup.variant')}</label>
                      <select id={`head-setup-variant-${i.part}`} className="talk-in" value={i.variant || ''} disabled={running} onChange={(e) => setVariants((x) => ({ ...x, [i.part]: e.target.value }))}>
                        {i.variants.map((v) => <option key={v.model} value={v.model}>{v.memory_bytes ? `${v.model} (${formatBytes(v.memory_bytes)})` : v.model}</option>)}
                      </select>
                    </div>
                  )}
                </>
              )))}
              {row('pipeline', target.name)}
            </ul>
            <p className="talk-help" data-testid="head-setup-total">
              {toInstall.length ? t(sizeUnknown(items) ? 'setup.totalPartial' : 'setup.total', { count: toInstall.length, size: formatBytes(totalBytes(items)) || '0 B' }) : t('setup.nothing')}
            </p>
          </>
        )}
      </section>

      {needsLicence && (
        <section className="talk-sec" data-testid="head-setup-licence">
          <h3>{t('setup.licenceTitle')}</h3>
          <p className="talk-help">{t('setup.licenceBody')}</p>
          <label className="head-setup__agree">
            <input type="checkbox" checked={agreed} disabled={running} onChange={(e) => setAgreed(e.target.checked)} data-testid="head-setup-agree" />
            <span>{t('setup.licenceAgree')}</span>
          </label>
        </section>
      )}

      <section className="talk-sec">
        {phase === 'done' && <p className="talk-help" role="status" data-testid="head-setup-done">{t('setup.doneBody', { name: target.name })}</p>}
        {phase === 'failed' && <p className="head-setup__error" role="alert">{t('setup.failedBody')}</p>}
        {phase === 'cancelled' && <p className="talk-help" role="status">{t('setup.cancelledBody')}</p>}
        <div className="talk-card__acts">
          {running ? (
            <button type="button" className="talk-btn talk-btn--end" onClick={() => abortRef.current?.abort()} data-testid="head-setup-cancel">
              <Icon name="stop" /> {t('setup.cancel')}
            </button>
          ) : phase === 'done' ? (
            <button type="button" className="talk-btn head-btn--primary" onClick={onClose} data-testid="head-setup-close">
              <Icon name="check" /> {t('setup.close')}
            </button>
          ) : (
            <button
              type="button"
              className="talk-btn head-btn--primary"
              disabled={!plan || blocked || Boolean(planError) || (needsLicence && !agreed)}
              onClick={start}
              data-testid="head-setup-start"
            >
              <Icon name={phase === 'failed' || phase === 'cancelled' ? 'refresh' : 'download'} /> {phase === 'failed' || phase === 'cancelled' ? t('setup.retry') : nothingToDo ? t('setup.use') : t('setup.install')}
            </button>
          )}
        </div>
      </section>
    </Sheet>
  )
}
