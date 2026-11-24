/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useMemo, useRef, useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import Icon from '../Icon'
import LoadingSpinner from '../LoadingSpinner'
import { Button, Segmented, StatusChip } from '../ui'
import { FileFooter, ProblemDetails } from './WellKnownCard'
import { useWellKnown } from '../../hooks/useWellKnown'
import { useModels } from '../../hooks/useModels'
import { ASK, DEFAULT_KINDS, defaultChoice, modelsFor, rowState, valueFor } from '../../utils/defaults'
import { parseSource, sourceText, wellKnownProblem, WELL_KNOWN_FILES } from '../../utils/wellKnown'
import './wellKnown.css'
import './defaults.css'

function timeLabel(d, locale) {
  try { return d.toLocaleTimeString(locale, { hour: 'numeric', minute: '2-digit' }) } catch { return '' }
}

// The room the picker keeps for the backend chip inside it, by the length of
// the backend name (the chip sits left of the arrow, over the select's padding).
function chipRoom(backend) {
  if (backend.length <= 8) return 'sm'
  return backend.length <= 11 ? 'md' : 'lg'
}

// One row: the kind of work, the model picker and "Ask each time". The picker
// lists the installed models that can do this kind of work. A model that is
// named in the file but not installed shows as missing: the assistant asks
// which model to use until the user picks another.
function DefaultRow({ kind, choice, installed, busy, onPick, onMode }) {
  const { t } = useTranslation('workspace')
  const state = rowState(choice, installed)
  const asking = state === 'ask'
  const label = t(`defaults.kinds.${kind.key}.label`)
  const selected = choice.mode === 'model' ? choice.model : ''
  const backend = installed.find(m => m.id === selected)?.backend || ''
  const options = installed.map(m => ({ value: m.id, label: m.id }))
  if (selected && !options.some(o => o.value === selected)) options.unshift({ value: selected, label: selected })

  return (
    <li className={`df-row df-row--${state}`} data-testid={`df-row-${kind.key}`} data-state={state}>
      <span className="df-row__icon" aria-hidden="true"><Icon name={kind.icon} /></span>
      <span className="df-row__label" id={`df-${kind.key}-label`}>{label}</span>
      <div className="df-row__meta">
        <span className="df-row__use" id={`df-${kind.key}-hint`}>{t(`defaults.kinds.${kind.key}.use`)}</span>
        {state === 'missing' && <span className="df-row__warn" data-testid={`df-missing-${kind.key}`}>{t('defaults.missing')}</span>}
        {choice.from === 'ui' && <span className="df-row__note" data-testid={`df-old-${kind.key}`}>{t('defaults.fromOld')}</span>}
      </div>
      {state === 'none' ? (
        <>
          <div className="df-pick df-pick--empty" data-testid={`df-none-${kind.key}`}>
            <span>{t('defaults.noModel')}</span>
            <StatusChip tone="warn" icon="warning">{t('defaults.notInstalled')}</StatusChip>
          </div>
          <Button size="sm" icon="plus" to="/app/models" data-testid={`df-install-${kind.key}`}>{t('defaults.installOne')}</Button>
        </>
      ) : (
        <>
          <div className={`df-pick${state === 'model' && backend ? ` df-pick--chip-${chipRoom(backend)}` : ''}${state === 'missing' ? ' df-pick--chip-lg' : ''}`}>
            <span className="dk-select-wrap">
              <select
                className="dk-select df-pick__select" value={selected} disabled={asking || busy}
                aria-labelledby={`df-${kind.key}-label`} aria-describedby={`df-${kind.key}-hint`}
                data-testid={`df-select-${kind.key}`}
                onChange={e => onPick(kind.key, e.target.value)}
              >
                <option value="">{asking ? t('defaults.asking') : t('defaults.automatic')}</option>
                {options.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
              </select>
            </span>
            {state === 'missing' && <StatusChip className="df-pick__chip" tone="warn" icon="warning">{t('defaults.notInstalled')}</StatusChip>}
            {state === 'model' && backend && <span className="df-chip df-pick__chip" data-testid={`df-backend-${kind.key}`}>{backend}</span>}
          </div>
          <Segmented
            className="df-mode" label={t('defaults.modeLabel', { kind: label })}
            value={asking ? 'ask' : 'default'} itemTestId={`df-mode-${kind.key}-`}
            options={[
              { id: 'default', label: t('defaults.mode.default'), disabled: busy },
              { id: 'ask', label: t('defaults.mode.ask'), disabled: busy },
            ]}
            onChange={id => onMode(kind.key, id)}
          />
        </>
      )}
    </li>
  )
}

// The side card: what the assistant does with the file, with the file itself.
function ReadsCard({ data, image }) {
  const { t } = useTranslation('workspace')
  const model = image.mode === 'model' ? image.model : t('defaults.help.exampleModel')
  return (
    <aside className="df-side" data-testid="df-help">
      <section className="df-help">
        <h3><Icon name="file-text" /> {t('defaults.help.title')}</h3>
        <p>{t('defaults.help.body')}</p>
        <p className="df-help__bubble">{t('defaults.help.example')}</p>
        <p className="df-help__tool"><Icon name="check" /> <code>{t('defaults.help.tool')}</code></p>
        <p><Trans t={t} i18nKey="defaults.help.used" values={{ model }} components={{ code: <code /> }} /></p>
      </section>
      <section>
        <h3 className="df-side__label">{t('defaults.preview')}</h3>
        <pre className="df-json" data-testid="df-json">{sourceText(data).trim()}</pre>
      </section>
    </aside>
  )
}

// DefaultsTab is Account > Defaults: LocalAI/defaults.json. A pick saves at
// once, like a switch, and writes defaults.json only; the older defaultModel
// of ui.json is read for the chat row and never written.
export default function DefaultsTab() {
  const { t, i18n } = useTranslation('workspace')
  const wk = useWellKnown('defaults')
  const ui = useWellKnown('ui')
  const { models, loading: modelsLoading, error: modelsError } = useModels()
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState(null)
  const [savedAt, setSavedAt] = useState(null)
  const [sourceOpen, setSourceOpen] = useState(false)
  const [source, setSource] = useState('')
  // The model of a row before the user chose "Ask each time", so choosing
  // "Default" again brings it back.
  const remembered = useRef({})
  const { doc } = wk
  const data = useMemo(() => doc?.data || {}, [doc])
  const uiData = ui.doc?.data

  const save = useCallback(async (values, opts = {}) => {
    setBusy(true)
    setProblem(null)
    try {
      try {
        await wk.save(values, opts)
      } catch (e) {
        // Someone changed another key meanwhile (the assistant, another tab):
        // the keys of a save are the whole edit, so it goes on top of the newer file.
        if (!e.conflict || opts.replace) throw e
        await wk.save(values, { version: e.current.version ?? 0 })
      }
      setSavedAt(new Date())
      return true
    } catch (e) {
      if (e.conflict) setProblem({ message: t('wellKnown.conflict'), errors: [] })
      else setProblem(wellKnownProblem(e, t))
      return false
    } finally {
      setBusy(false)
    }
  }, [wk, t])

  const onPick = (key, model) => {
    if (model) remembered.current[key] = model
    save({ [key]: valueFor(model) })
  }
  const onMode = (key, mode) => {
    const choice = defaultChoice(data, uiData, key)
    if (mode === 'ask') {
      if (choice.mode === 'model') remembered.current[key] = choice.model
      save({ [key]: ASK })
    } else {
      save({ [key]: valueFor(remembered.current[key] || '') })
    }
  }

  const revert = async () => {
    setBusy(true)
    setProblem(null)
    try { await wk.revert(); setSavedAt(new Date()) } catch (e) { setProblem(wellKnownProblem(e, t)) } finally { setBusy(false) }
  }
  const openSource = () => {
    if (!sourceOpen) setSource(sourceText(data))
    setSourceOpen(open => !open)
  }
  const saveSource = async () => {
    const parsed = parseSource(source)
    if (parsed.error) { setProblem({ message: t(`wellKnown.source.${parsed.error}`), errors: parsed.detail ? [parsed.detail] : [] }); return }
    if (await save(parsed.value, { replace: true, version: doc?.version ?? 0 })) setSourceOpen(false)
  }

  const kinds = useMemo(() => DEFAULT_KINDS.map(k => ({ kind: k, installed: modelsFor(models, k.cap) })), [models])

  return (
    <section className="df" data-testid="account-defaults" aria-labelledby="df-title">
      <h2 id="df-title" className="df__lede">{t('defaults.lede')}</h2>
      {wk.error && !doc && (
        <div className="wk-note wk-note--error" role="alert"><Icon name="alert-circle" /><div><p>{t('wellKnown.loadFailed')}</p><ProblemDetails errors={[wk.error.message]} /></div></div>
      )}
      {modelsError && <div className="wk-note wk-note--warn" role="status"><Icon name="warning" /><div><p>{t('defaults.modelsFailed')}</p></div></div>}
      {doc && !doc.valid && (
        <div className="wk-note wk-note--warn" role="status" data-testid="wk-invalid-defaults">
          <Icon name="warning" />
          <div>
            <p>{doc.fallback ? t('wellKnown.invalidFallback') : t('wellKnown.invalid')}</p>
            <ProblemDetails errors={doc.errors} />
          </div>
        </div>
      )}
      {problem && (
        <div className="wk-note wk-note--error" role="alert" data-testid="wk-problem-defaults">
          <Icon name="alert-circle" />
          <div><p>{problem.message}</p><ProblemDetails errors={problem.errors} /></div>
        </div>
      )}

      {!doc || modelsLoading ? (
        <div className="wk-loading"><LoadingSpinner size="sm" /></div>
      ) : (
        <div className="df__body">
          <div className="df__main">
            <ul className="df-rows">
              {kinds.map(({ kind, installed }) => (
                <DefaultRow
                  key={kind.key} kind={kind} installed={installed} busy={busy}
                  choice={defaultChoice(data, uiData, kind.key)} onPick={onPick} onMode={onMode}
                />
              ))}
            </ul>
            {sourceOpen && (
              <div className="df-source dk-field">
                <label className="dk-label" htmlFor="df-source-text">{t('wellKnown.source.label')}</label>
                <textarea id="df-source-text" className="dk-input dk-textarea dk-mono wk-source" spellCheck={false} value={source} onChange={e => setSource(e.target.value)} data-testid="df-source-text" />
                <p className="dk-hint">{t('wellKnown.source.hint')}</p>
                <div className="wk-actions">
                  <Button variant="primary" size="sm" icon="save" onClick={saveSource} busy={busy} data-testid="df-source-save">{t('wellKnown.save')}</Button>
                  <Button size="sm" variant="ghost" onClick={() => setSourceOpen(false)}>{t('wellKnown.cancel')}</Button>
                </div>
              </div>
            )}
          </div>
          <ReadsCard data={data} image={defaultChoice(data, uiData, 'image')} />
        </div>
      )}

      <FileFooter
        name="defaults" file={WELL_KNOWN_FILES.defaults} onSource={openSource} sourceOpen={sourceOpen}
        onRevert={revert} canRevert={!busy && !!doc?.exists}
      >
        {savedAt && <span className="wk-stored__saved" role="status" data-testid="df-saved">{t('wellKnown.savedAt', { time: timeLabel(savedAt, i18n.language) })}</span>}
      </FileFooter>
    </section>
  )
}
