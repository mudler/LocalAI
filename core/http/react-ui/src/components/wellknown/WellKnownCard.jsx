/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import LoadingSpinner from '../LoadingSpinner'
import SchemaFields from './SchemaFields'
import { useWellKnown, loadSchema } from '../../hooks/useWellKnown'
import { fieldsFromSchema, changedValues, parseSource, sourceText } from '../../utils/wellKnown'
import './wellKnown.css'

function timeLabel(d, locale) {
  try { return d.toLocaleTimeString(locale, { hour: 'numeric', minute: '2-digit' }) } catch { return '' }
}

// StoredIn is the line that names the file behind a setting, with the revert.
export function StoredIn({ file, onRevert, canRevert, busy }) {
  const { t } = useTranslation('workspace')
  return (
    <div className="wk-meta">
      <span className="wk-src"><Icon name="file" /> {t('wellKnown.storedIn')} <code>{file}</code></span>
      {onRevert && (
        <button type="button" className="wk-linkbtn" onClick={onRevert} disabled={!canRevert || busy} data-testid={`wk-revert-${file}`}>
          <Icon name="undo" /> {t('wellKnown.revert')}
        </button>
      )}
    </div>
  )
}

// WellKnownCard edits one well-known file: a form drawn from its schema, a
// Source view of the raw JSON with the same validation, the file name with
// a revert to the previous version, and the answer to a conflict.
//
//   prefill(doc)  -> { values, notes }: values to show for keys the file does
//                    not have (they are saved with the next Save), and the
//                    line under such a field
//   onSaved(doc)  after a save or a revert
export default function WellKnownCard({ name, title, lede, prefill, options, onSaved, onChange, testId, children }) {
  const { t, i18n } = useTranslation('workspace')
  const wk = useWellKnown(name)
  const { doc } = wk
  const [schema, setSchema] = useState(null)
  const [schemaError, setSchemaError] = useState(null)
  const [values, setValues] = useState({})
  const [dirty, setDirty] = useState(false)
  const [mode, setMode] = useState('form')
  const [source, setSource] = useState('')
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState(null) // { message, errors }
  const [conflict, setConflict] = useState(null)
  const [savedAt, setSavedAt] = useState(null)
  const baseRef = useRef({})

  useEffect(() => {
    let live = true
    loadSchema(name).then(s => { if (live) setSchema(s) }).catch(e => { if (live) setSchemaError(e) })
    return () => { live = false }
  }, [name])

  const fields = useMemo(() => fieldsFromSchema(schema), [schema])
  // fileData is what the file holds: the server marks the values it filled in
  // itself (the account's name) as prefilled, and those are not in the file.
  const fileData = useMemo(() => {
    const d = { ...(doc?.data || {}) }
    for (const k of Object.keys(doc?.prefilled || {})) delete d[k]
    return d
  }, [doc])
  const filled = useMemo(() => {
    const out = { values: {}, notes: {} }
    for (const [k, from] of Object.entries(doc?.prefilled || {})) {
      out.values[k] = doc.data[k]
      out.notes[k] = t(`wellKnown.from.${from}`, { defaultValue: from })
    }
    if (prefill && doc) {
      const p = prefill(doc, fileData)
      for (const [k, v] of Object.entries(p.values || {})) {
        if (fileData[k] === undefined || fileData[k] === '') { out.values[k] = v; out.notes[k] = p.notes?.[k] || out.notes[k] }
      }
    }
    return out
  }, [prefill, doc, fileData, t])

  // Take the file's values whenever it changes and the form holds no edit of
  // its own, so a change by the assistant shows at once.
  useEffect(() => {
    if (!doc || dirty) return
    baseRef.current = fileData
    setValues({ ...fileData, ...filled.values })
    setSource(sourceText(fileData))
  }, [doc, fileData, filled, dirty])

  useEffect(() => { onChange?.(values) }, [values, onChange])

  const set = (k, v) => { setValues(prev => ({ ...prev, [k]: v })); setDirty(true); setProblem(null) }

  const pending = useMemo(() => changedValues(fields, baseRef.current, values), [fields, values])
  const canSave = mode === 'source' ? dirty : Object.keys(pending).length > 0

  const done = (d) => {
    setDirty(false)
    setConflict(null)
    setProblem(null)
    setSavedAt(new Date())
    onSaved?.(d)
  }

  const save = async () => {
    setBusy(true)
    setProblem(null)
    try {
      let d
      if (mode === 'source') {
        const parsed = parseSource(source)
        if (parsed.error) { setProblem({ message: t(`wellKnown.source.${parsed.error}`), errors: parsed.detail ? [parsed.detail] : [] }); return }
        d = await wk.save(parsed.value, { replace: true })
      } else {
        d = await wk.save(pending)
      }
      done(d)
    } catch (e) {
      if (e.conflict) setConflict(e.current)
      else setProblem({ message: e.message, errors: e.errors || [] })
    } finally {
      setBusy(false)
    }
  }

  // Keep mine: take the newer version as the base and save the edit on it.
  const keepMine = async () => {
    const cur = conflict
    setConflict(null)
    baseRef.current = cur?.data || {}
    setBusy(true)
    try {
      const d = mode === 'source'
        ? await wk.save(parseSource(source).value || {}, { replace: true, version: cur?.version ?? 0 })
        : await wk.save(changedValues(fields, cur?.data || {}, values), { version: cur?.version ?? 0 })
      done(d)
    } catch (e) {
      if (e.conflict) setConflict(e.current)
      else setProblem({ message: e.message, errors: e.errors || [] })
    } finally { setBusy(false) }
  }
  const useTheirs = async () => { setConflict(null); setDirty(false); await wk.reload() }

  const revert = async () => {
    setBusy(true)
    try { done(await wk.revert()) } catch (e) { setProblem({ message: e.message, errors: [] }) } finally { setBusy(false) }
  }

  const file = `${name}.json`
  const status = (() => {
    if (savedAt) return t('wellKnown.savedAt', { time: timeLabel(savedAt, i18n.language) })
    if (doc && !doc.exists && Object.keys(filled.values).length > 0) return t('wellKnown.notSavedPrefilled')
    if (doc && !doc.exists) return t('wellKnown.notSaved')
    return ''
  })()

  return (
    <section className="wk-card" data-testid={testId || `wk-card-${name}`} aria-labelledby={`wk-${name}-title`}>
      <header className="wk-card__head">
        <div className="wk-card__titles">
          <h2 id={`wk-${name}-title`}>{title}</h2>
          {lede && <p>{lede}</p>}
        </div>
        <div className="dk-segmented wk-modes" role="radiogroup" aria-label={t('wellKnown.view')}>
          <button type="button" role="radio" aria-checked={mode === 'form'} className="dk-seg" onClick={() => setMode('form')}>{t('wellKnown.form')}</button>
          <button type="button" role="radio" aria-checked={mode === 'source'} className="dk-seg" data-testid={`wk-source-${name}`} onClick={() => { setSource(dirty && mode === 'form' ? sourceText({ ...fileData, ...values }) : source); setMode('source') }}>{t('wellKnown.source.label')}</button>
        </div>
      </header>
      <StoredIn file={file} onRevert={revert} canRevert={!!doc?.exists} busy={busy} />

      {wk.error && !doc && (
        <p className="wk-note wk-note--error" role="alert"><Icon name="alert-circle" /> {t('wellKnown.loadFailed', { message: wk.error.message })}</p>
      )}
      {schemaError && <p className="wk-note wk-note--error" role="alert"><Icon name="alert-circle" /> {t('wellKnown.loadFailed', { message: schemaError.message })}</p>}
      {doc && !doc.valid && (
        <div className="wk-note wk-note--warn" role="status" data-testid={`wk-invalid-${name}`}>
          <Icon name="warning" />
          <div>
            <p>{doc.fallback ? t('wellKnown.invalidFallback', { file }) : t('wellKnown.invalid', { file })}</p>
            <ul>{(doc.errors || []).map(e => <li key={e}><code>{e}</code></li>)}</ul>
          </div>
        </div>
      )}
      {conflict && (
        <div className="wk-note wk-note--warn" role="alert" data-testid={`wk-conflict-${name}`}>
          <Icon name="swap" />
          <div>
            <p>{t('wellKnown.conflict', { file })}</p>
            <dl className="wk-diff">
              {fields.filter(f => (conflict.data || {})[f.key] !== values[f.key]).map(f => (
                <div key={f.key}><dt>{t(`wellKnown.fields.${name}.${f.key}.label`, { defaultValue: f.label })}</dt><dd>{String((conflict.data || {})[f.key] ?? '')}</dd></div>
              ))}
            </dl>
            <div className="wk-actions">
              <button type="button" className="dk-btn dk-btn--sm dk-btn--secondary" onClick={useTheirs}>{t('wellKnown.useTheirs')}</button>
              <button type="button" className="dk-btn dk-btn--sm dk-btn--primary" onClick={keepMine}>{t('wellKnown.keepMine')}</button>
            </div>
          </div>
        </div>
      )}

      {!doc && !wk.error ? (
        <div className="wk-loading"><LoadingSpinner size="sm" /></div>
      ) : (
        <form className="wk-form" onSubmit={(e) => { e.preventDefault(); if (canSave && !busy) save() }}>
          {mode === 'form' ? (
            <div className="wk-fields">
              <SchemaFields name={name} fields={fields} values={values} onChange={set} notes={filled.notes} options={options} disabled={busy} />
              {children}
            </div>
          ) : (
            <div className="dk-field">
              <label className="dk-label" htmlFor={`wk-${name}-source`}>{file}</label>
              <textarea
                id={`wk-${name}-source`} className="dk-input dk-textarea dk-mono wk-source" spellCheck={false} value={source}
                data-testid={`wk-source-text-${name}`}
                onChange={e => { setSource(e.target.value); setDirty(true); setProblem(null) }}
              />
              <p className="dk-hint">{t('wellKnown.source.hint')}</p>
            </div>
          )}
          {problem && (
            <div className="wk-note wk-note--error" role="alert" data-testid={`wk-problem-${name}`}>
              <Icon name="alert-circle" />
              <div>
                <p>{problem.message}</p>
                {problem.errors.length > 0 && <ul>{problem.errors.map(e => <li key={e}><code>{e}</code></li>)}</ul>}
              </div>
            </div>
          )}
          <div className="wk-actions">
            <button type="submit" className="dk-btn dk-btn--primary" disabled={!canSave || busy} data-testid={`wk-save-${name}`}>
              {busy ? <LoadingSpinner size="sm" /> : <Icon name="save" />} {t('wellKnown.save')}
            </button>
            {status && <span className="wk-status" role="status">{status}</span>}
          </div>
        </form>
      )}
    </section>
  )
}
