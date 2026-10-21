/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import LoadingSpinner from '../LoadingSpinner'
import ConfirmDialog from '../ConfirmDialog'
import SideSheet from '../SideSheet'
import CatalogEmpty from './CatalogEmpty'
import { SOURCE_KINDS, canRefresh, countByKind, createOperationBook, draftToLibrarySource, draftToSource, filterSources, slugify, sourceKind, sourceState, sourceToDraft, validateSourceDraft } from './catalogState'
import './catalog.css'
import '../addons/addons.css'

const BLANK = { kind: 'addon', format: 'library', id: '', name: '', url: '', enabled: true, requireSignature: false, issuer: '', identity: '', identityRegex: '', authReference: '', allowLoopbackHTTP: false }

function seen(iso) {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) || d.getFullYear() < 2000 ? null : d
}

// The library sources of all three kinds (add-ons, backends and models): list
// with a kind filter, add, turn on and off, edit, refresh, remove. Resource
// neutral. The adapter owns what a save means; this view owns the form, the
// operation identity of each action and honest reporting of its outcome.
//
//   adapter        { listSources, saveSource(source, signal, operationId),
//                    refreshSource(id, signal, operationId),
//                    addSource(body, signal), setSourceEnabled(id, on, signal),
//                    removeSource(id, signal) }
//   describeError  (error) => string
//   onChanged      called after a change was confirmed
//
// A library is saved with a credential REFERENCE by name. The form has no field
// for a secret, and the server refuses anything that is not a reference.
export default function SourcesView({ adapter, describeError, onChanged }) {
  const { t, i18n } = useTranslation('addons')
  const [state, setState] = useState({ status: 'loading', sources: [], error: null })
  const [reload, setReload] = useState(0)
  const [editing, setEditing] = useState(null)
  const [removing, setRemoving] = useState(null)
  const [kind, setKind] = useState('all')
  const [busy, setBusy] = useState({})
  const [notice, setNotice] = useState(null)
  const book = useRef(null)
  if (book.current === null) book.current = createOperationBook()
  const abort = useRef(null)

  useEffect(() => {
    abort.current = new AbortController()
    return () => abort.current.abort()
  }, [])

  useEffect(() => {
    const ctl = new AbortController()
    adapter.listSources(ctl.signal)
      .then(sources => setState({ status: 'ready', sources, error: null }))
      .catch(error => { if (!ctl.signal.aborted) setState(s => ({ status: s.status === 'ready' ? 'ready' : 'error', sources: s.sources, error })) })
    return () => ctl.abort()
  }, [adapter, reload])

  // Run one write with its own identity. A refusal ends the action; a lost
  // answer keeps the identity, so the retry replays the same operation.
  const act = useCallback(async (key, write, okMessage) => {
    const operationId = book.current.idFor(key)
    setBusy(b => ({ ...b, [key]: true }))
    setNotice(null)
    try {
      await write(operationId, abort.current.signal)
      book.current.done(key)
      setNotice({ tone: 'ok', text: okMessage })
      if (onChanged) onChanged()
      return true
    } catch (e) {
      if (e && e.name === 'AbortError') return false
      if (e.kind === 'denied' || e.kind === 'invalid' || e.kind === 'auth') book.current.done(key)
      setNotice({ tone: 'error', text: describeError(e) })
      return false
    } finally {
      setBusy(b => { const next = { ...b }; delete next[key]; return next })
      setReload(n => n + 1)
    }
  }, [onChanged, describeError])

  const save = async (draft) => {
    // A backend or model source is added through the library route; an
    // add-on source keeps its form with a policy and a credential reference.
    const write = draft.isNew && draft.kind !== 'addon'
      ? (operationId, signal) => adapter.addSource(draftToLibrarySource(draft), signal)
      : (operationId, signal) => adapter.saveSource(draftToSource(draft), signal, operationId)
    const ok = await act(`save:${draft.id || draft.url}`, write, t('libraries.saved', { name: draft.name || draft.url }))
    if (ok) setEditing(null)
    return ok
  }
  const remove = async (source) => {
    setRemoving(null)
    await act(`remove:${source.id}`, (operationId, signal) => adapter.removeSource(source.id, signal),
      source.default ? t('libraries.removedDefault', { name: source.name }) : t('libraries.removed', { name: source.name }))
  }
  const toggle = (source, on) => act(`toggle:${source.id}`, (operationId, signal) => adapter.setSourceEnabled(source.id, on, signal),
    on ? t('libraries.turnedOn', { name: source.name }) : t('libraries.turnedOff', { name: source.name }))
  const refresh = (source) => act(`refresh:${source.id}`, (operationId, signal) => adapter.refreshSource(source.id, signal, operationId), t('libraries.refreshed', { name: source.name }))

  const counts = countByKind(state.sources)
  const shown = filterSources(state.sources, kind)

  if (state.status === 'loading') return <div className="op-loading" role="status" aria-label={t('libraries.loading')}><LoadingSpinner size="lg" /></div>
  if (state.status === 'error') {
    return (
      <div className="op-error" role="alert">
        <Icon name="alert-circle" />
        <span>{describeError(state.error)}</span>
        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => { setState({ status: 'loading', sources: [], error: null }); setReload(n => n + 1) }}>
          <Icon name="refresh" /> {t('catalog.retry')}
        </button>
      </div>
    )
  }

  return (
    <div className="cat-sources">
      <div className="cat-sources__bar">
        <p className="op-note op-note--quiet">{t('libraries.lead')}</p>
        <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" onClick={() => setEditing({ ...BLANK, isNew: true })}>
          <Icon name="plus" /> {t('libraries.add')}
        </button>
      </div>
      {notice && (
        <div className={notice.tone === 'error' ? 'op-error' : 'op-notice'} role={notice.tone === 'error' ? 'alert' : 'status'}>
          <Icon name={notice.tone === 'error' ? 'alert-circle' : 'check-circle'} />
          <span>{notice.text}</span>
        </div>
      )}
      {state.sources.some(x => x.suspended) && (
        <p className="op-note op-note--quiet" data-testid="official-off">{t('libraries.officialOff')}</p>
      )}
      {state.sources.length > 0 && (
        <div className="cat-chips" role="group" aria-label={t('libraries.kindFilter')}>
          {['all', ...SOURCE_KINDS].map(k => (
            <button key={k} type="button" className="dk-chip" data-kind-filter={k} aria-pressed={kind === k} onClick={() => setKind(k)}>
              {t(`libraries.kinds.${k}`)} <span className="dk-mono">{counts[k]}</span>
            </button>
          ))}
        </div>
      )}
      {state.sources.length === 0 ? (
        <CatalogEmpty icon="store" title={t('libraries.emptyTitle')} text={t('libraries.emptyBody')} />
      ) : shown.length === 0 ? (
        <CatalogEmpty icon="store" title={t('libraries.emptyKindTitle')} text={t('libraries.emptyKindBody')} />
      ) : (
        <ul className="cat-list" aria-label={t('libraries.listLabel')}>
          {shown.map(source => {
            const last = source.refresh ? seen(source.refresh.lastSuccess) : null
            const st = sourceState(source)
            const sk = sourceKind(source)
            const working = Boolean(busy[`refresh:${source.id}`] || busy[`remove:${source.id}`] || busy[`save:${source.id}`] || busy[`toggle:${source.id}`])
            return (
              <li key={source.id} className="cat-item cat-source" data-source={source.id} data-kind={sk} data-state={st}>
                <div className="cat-source__body">
                  <h3 className="cat-source__title">{source.name} <span className="dk-mono cat-source__id">{source.id}</span></h3>
                  <p className="cat-source__url dk-mono">{source.url}</p>
                  <div className="ad-facts">
                    <span className="ad-fact" data-testid="source-kind" data-kind={sk}>{t(`libraries.kind.${sk}`)}</span>
                    {source.default && <span className="ad-fact" data-testid="source-default">{t('libraries.default')}</span>}
                    <span className="ad-fact" data-testid="source-state" data-tone={st === 'enabled' ? 'ok' : undefined}>{t(`libraries.state.${st}`)}</span>
                    {source.format === 'gallery' && <span className="ad-fact">{t('libraries.gallery')}</span>}
                    {source.policy?.requireSignature && <span className="ad-fact" data-tone="ok"><Icon name="shield" aria-hidden="true" />{t('trust.signatureRequired')}</span>}
                    {source.authReference && <span className="ad-fact"><Icon name="key" aria-hidden="true" />{t('libraries.reference', { name: source.authReference })}</span>}
                    {source.refresh?.failed && <span className="ad-fact" data-tone="error">{t('libraries.refreshFailed')}</span>}
                    {!source.refresh && canRefresh(source) && <span className="ad-fact">{t('libraries.notRefreshed')}</span>}
                    {source.refresh?.skipped > 0 && !source.refresh.failed && (
                      <span className="ad-fact" data-tone="warn" data-testid="skipped-kinds">{t('libraries.skipped', { count: source.refresh.skipped, kinds: (source.refresh.skippedKinds || []).join(', ') })}</span>
                    )}
                    {last && !source.refresh.failed && <span className="ad-fact">{t('libraries.lastRefresh', { when: last.toLocaleString(i18n.language), count: source.refresh.count })}</span>}
                  </div>
                  {source.conflict && (
                    <p className="cat-source__conflict" role="note" data-testid="source-conflict"><Icon name="alert-circle" aria-hidden="true" /> {source.conflict}</p>
                  )}
                </div>
                <div className="cat-source__acts">
                  {st === 'removed' ? (
                    <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" data-action="restore" disabled={working} onClick={() => toggle(source, true)}>
                      <Icon name="undo" /> {t('libraries.restore')}
                    </button>
                  ) : (
                    <>
                      {st !== 'suspended' && (
                        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" data-action={st === 'enabled' ? 'disable' : 'enable'} disabled={working} onClick={() => toggle(source, st !== 'enabled')}>
                          <Icon name={st === 'enabled' ? 'ban' : 'check-circle'} /> {st === 'enabled' ? t('libraries.turnOff') : t('libraries.turnOn')}
                        </button>
                      )}
                      <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" disabled={working || !canRefresh(source)} onClick={() => refresh(source)}>
                        <Icon name="refresh" spin={Boolean(busy[`refresh:${source.id}`])} /> {t('libraries.refresh')}
                      </button>
                      {sk === 'addon' && (
                        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" disabled={working} onClick={() => setEditing(sourceToDraft(source))}>
                          <Icon name="pencil" /> {t('libraries.edit')}
                        </button>
                      )}
                      <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" data-action="remove" disabled={working} onClick={() => setRemoving(source)}>
                        <Icon name="trash" /> {t('libraries.remove')}
                      </button>
                    </>
                  )}
                </div>
              </li>
            )
          })}
        </ul>
      )}
      {editing && <SourceEditor draft={editing} onSave={save} onClose={() => setEditing(null)} saving={Boolean(busy[`save:${editing.id}`])} t={t} />}
      <ConfirmDialog
        open={Boolean(removing)}
        danger
        title={removing ? t('libraries.removeTitle', { name: removing.name }) : ''}
        message={<p>{removing?.default ? t('libraries.removeDefaultBody') : t('libraries.removeBody')}</p>}
        confirmLabel={t('libraries.remove')}
        onConfirm={() => remove(removing)}
        onCancel={() => setRemoving(null)}
      />
    </div>
  )
}

function SourceEditor({ draft, onSave, onClose, saving, t }) {
  const [form, setForm] = useState(draft)
  const [touched, setTouched] = useState(false)
  const isNew = Boolean(draft.isNew)
  const addon = form.kind === 'addon'
  const errors = touched ? validateSourceDraft(form) : {}
  const set = (patch) => setForm(f => ({ ...f, ...patch }))
  const submit = (e) => {
    e.preventDefault()
    setTouched(true)
    if (Object.keys(validateSourceDraft(form)).length === 0) onSave(form)
  }
  const field = (name, label, props = {}, hint) => (
    <div className="dk-field">
      <label className="dk-label" htmlFor={`src-${name}`}>{label}</label>
      <input id={`src-${name}`} className="dk-input" value={form[name]} aria-invalid={errors[name] ? 'true' : undefined} aria-describedby={errors[name] ? `src-${name}-err` : hint ? `src-${name}-hint` : undefined} onChange={e => set({ [name]: e.target.value })} {...props} />
      {hint && !errors[name] && <p className="dk-field-hint" id={`src-${name}-hint`}>{hint}</p>}
      {errors[name] && <p className="dk-field-error" id={`src-${name}-err`} role="alert">{t(`libraries.errors.${errors[name]}`)}</p>}
    </div>
  )
  return (
    <SideSheet
      title={isNew ? t('libraries.addTitle') : t('libraries.editTitle', { name: draft.name })}
      onClose={onClose}
      closeLabel={t('catalog.close')}
      labelId="source-editor-title"
      testId="source-editor"
      footer={(
        <>
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={onClose}>{t('catalog.cancel')}</button>
          <button type="submit" form="source-editor-form" className="dk-btn dk-btn--primary dk-btn--sm" disabled={saving}>
            <Icon name={saving ? 'spinner' : 'save'} spin={saving} /> {t('libraries.save')}
          </button>
        </>
      )}
    >
      <form id="source-editor-form" className="cat-form" onSubmit={submit} noValidate>
        {isNew && (
          <div className="dk-field">
            <label className="dk-label" htmlFor="src-kind">{t('libraries.fields.kind')}</label>
            <select id="src-kind" className="dk-select" value={form.kind} onChange={e => set({ kind: e.target.value })}>
              {SOURCE_KINDS.map(k => <option key={k} value={k}>{t(`libraries.kind.${k}`)}</option>)}
            </select>
          </div>
        )}
        {isNew && form.kind !== 'addon' && (
          <div className="dk-field">
            <label className="dk-label" htmlFor="src-format">{t('libraries.fields.format')}</label>
            <select id="src-format" className="dk-select" value={form.format} onChange={e => set({ format: e.target.value })}>
              <option value="library">{t('libraries.formats.library')}</option>
              <option value="gallery">{t('libraries.formats.gallery')}</option>
            </select>
            <p className="dk-field-hint">{t('libraries.hints.format')}</p>
          </div>
        )}
        {field('name', t('libraries.fields.name'), { autoComplete: 'off', onChange: e => set({ name: e.target.value, ...(isNew && !form.idTouched ? { id: slugify(e.target.value) } : {}) }) })}
        {field('id', t('libraries.fields.id'), { autoComplete: 'off', readOnly: !isNew, onChange: e => set({ id: e.target.value, idTouched: true }) }, t('libraries.hints.id'))}
        {field('url', t('libraries.fields.url'), { type: 'url', autoComplete: 'off', inputMode: 'url', placeholder: 'https://' }, t('libraries.hints.url'))}
        {addon && (
          <label className="cat-check">
            <input type="checkbox" checked={form.enabled} onChange={e => set({ enabled: e.target.checked })} />
            <span>{t('libraries.fields.enabled')}</span>
          </label>
        )}
        {addon && (
          <label className="cat-check">
            <input type="checkbox" checked={form.requireSignature} onChange={e => set({ requireSignature: e.target.checked })} />
            <span>{t('libraries.fields.requireSignature')}</span>
          </label>
        )}
        {addon && form.requireSignature && field('issuer', t('libraries.fields.issuer'), { autoComplete: 'off' })}
        {addon && form.requireSignature && field('identity', t('libraries.fields.identity'), { autoComplete: 'off' })}
        {addon && form.requireSignature && field('identityRegex', t('libraries.fields.identityRegex'), { autoComplete: 'off', spellCheck: false }, t('libraries.hints.identityRegex'))}
        {addon && field('authReference', t('libraries.fields.reference'), { autoComplete: 'off', spellCheck: false }, t('libraries.hints.reference'))}
      </form>
    </SideSheet>
  )
}
