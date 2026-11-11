/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import LoadingSpinner from '../LoadingSpinner'
import { FileFooter, ProblemDetails } from './WellKnownCard'
import { useWellKnown } from '../../hooks/useWellKnown'
import { usePages } from '../../hooks/usePages'
import { useSharedPages } from '../../hooks/useSharedPages'
import { useAuth } from '../../context/AuthContext'
import { compactOf, refKey } from '../../utils/pageTiles'
import {
  SLOT_CAPS, SLOT_LIMITS, SLOT_NAMES, canAddTo, cleanSlots, itemLabel, mergeSlots, readSlots, slotItemOk, slotKey, toolArguments,
} from '../../utils/slots'
import { move, parseSource, sourceText, wellKnownProblem, WELL_KNOWN_FILES } from '../../utils/wellKnown'
import './wellKnown.css'
import './navEditor.css'
import './slotsEditor.css'

const MAX_TRIES = 3
const SLOT_ICONS = { chatHeader: 'columns', sidebarFooter: 'menu', composerToolbar: 'terminal' }
const ADD_KINDS = [['page', 'layers'], ['tool', 'wrench'], ['command', 'code']]

let uid = 0
const withUid = (it) => (it._uid ? it : { ...it, _uid: ++uid })
const stripUid = (list) => list.map(({ _uid, ...it }) => it)

// The map of where the slots are: a small app with the three places marked,
// the selected one in the accent colour.
function SlotMap({ selected, onSelect, counts }) {
  const { t } = useTranslation('workspace')
  const place = (name, cls) => (
    <button
      type="button" className={`sl-map__slot sl-map__slot--${cls}`} aria-pressed={selected === name}
      onClick={() => onSelect(name)} data-testid={`slots-map-${name}`}
    >
      <span className="sl-map__tag">{t(`wellKnown.slots.names.${name}`)}</span>
      <span className="sl-map__count">{counts[name]}</span>
    </button>
  )
  return (
    <div className="sl-map" aria-label={t('wellKnown.slots.map')} role="group">
      <div className="sl-map__side">
        <span className="sl-map__line" /><span className="sl-map__line" /><span className="sl-map__line sl-map__line--short" />
        {place('sidebarFooter', 'side')}
      </div>
      <div className="sl-map__main">
        <div className="sl-map__bar"><span className="sl-map__line sl-map__line--title" />{place('chatHeader', 'head')}</div>
        <div className="sl-map__thread">
          <span className="sl-map__bubble sl-map__bubble--me" />
          <span className="sl-map__bubble" />
          <span className="sl-map__bubble sl-map__bubble--me sl-map__bubble--short" />
        </div>
        <div className="sl-map__composer"><span className="sl-map__line" />{place('composerToolbar', 'composer')}</div>
      </div>
    </div>
  )
}

// The fields of one item, by its type.
function ItemForm({ item, onChange, pages, t }) {
  const [argsText, setArgsText] = useState(() => (item.arguments ? JSON.stringify(item.arguments, null, 2) : ''))
  const [argsBad, setArgsBad] = useState(false)
  const set = (k, v) => {
    const next = { ...item }
    if (v === '' || v === undefined) delete next[k]
    else next[k] = v
    onChange(next)
  }
  const onArgs = (text) => {
    setArgsText(text)
    if (text.trim() === '') { setArgsBad(false); set('arguments', undefined); return }
    try {
      const v = JSON.parse(text)
      const ok = toolArguments(v)
      setArgsBad(!ok)
      if (ok) set('arguments', v)
    } catch { setArgsBad(true) }
  }
  const id = (k) => `sl-${item._uid}-${k}`
  return (
    <div className="sl-form" data-testid="slots-item-form">
      {item.type === 'page' && (
        <div className="dk-field">
          <label className="dk-label" htmlFor={id('page')}>{t('wellKnown.slots.form.page')}</label>
          <select
            id={id('page')} className="dk-select" value={refKey({ name: item.page || '', folder: item.folder || 0 })}
            onChange={(e) => {
              const p = pages.find((x) => x.key === e.target.value)
              const next = { ...item, page: p?.ref.name || '' }
              if (p?.ref.folder) next.folder = p.ref.folder
              else delete next.folder
              onChange(next)
            }}
            data-testid="slots-form-page"
          >
            <option value="">{t('wellKnown.slots.form.pickPage')}</option>
            {pages.map((p) => <option key={p.key} value={p.key}>{p.title}{p.ref.folder ? ` (${t('wellKnown.slots.form.shared')})` : ''}</option>)}
          </select>
          <p className="dk-hint">{t('wellKnown.slots.form.pageHint')}</p>
        </div>
      )}
      {item.type === 'tool' && (
        <>
          <div className="dk-field">
            <label className="dk-label" htmlFor={id('tool')}>{t('wellKnown.slots.form.tool')}</label>
            <input id={id('tool')} className="dk-input dk-mono" value={item.tool || ''} maxLength={128} onChange={(e) => set('tool', e.target.value.trim())} placeholder="summarize_chat" data-testid="slots-form-tool" />
          </div>
          <div className="dk-field">
            <label className="dk-label" htmlFor={id('args')}>{t('wellKnown.slots.form.arguments')}</label>
            <textarea id={id('args')} className="dk-input dk-textarea dk-mono sl-form__args" value={argsText} spellCheck={false} onChange={(e) => onArgs(e.target.value)} placeholder={'{ "words": 120 }'} aria-invalid={argsBad || undefined} data-testid="slots-form-arguments" />
            <p className={`dk-hint${argsBad ? ' sl-form__bad' : ''}`} role={argsBad ? 'alert' : undefined}>{t(argsBad ? 'wellKnown.slots.form.argumentsBad' : 'wellKnown.slots.form.argumentsHint', { max: SLOT_LIMITS.argumentKeys })}</p>
          </div>
        </>
      )}
      {item.type === 'command' && (
        <div className="dk-field">
          <label className="dk-label" htmlFor={id('command')}>{t('wellKnown.slots.form.command')}</label>
          <input id={id('command')} className="dk-input dk-mono" value={item.command || ''} maxLength={40} onChange={(e) => set('command', e.target.value.trim().replace(/^\//, ''))} placeholder="translate" data-testid="slots-form-command" />
          <p className="dk-hint">{t('wellKnown.slots.form.commandHint')}</p>
        </div>
      )}
      <div className="sl-form__row">
        <div className="dk-field">
          <label className="dk-label" htmlFor={id('label')}>{t(item.type === 'tool' ? 'wellKnown.slots.form.labelNeeded' : 'wellKnown.slots.form.label')}</label>
          <input id={id('label')} className="dk-input" value={item.label || ''} maxLength={SLOT_LIMITS.label} onChange={(e) => set('label', e.target.value)} data-testid="slots-form-label" />
        </div>
        <div className="dk-field">
          <label className="dk-label" htmlFor={id('icon')}>{t('wellKnown.slots.form.icon')}</label>
          <input id={id('icon')} className="dk-input dk-mono" value={item.icon || ''} maxLength={40} onChange={(e) => set('icon', e.target.value.trim().toLowerCase())} placeholder={item.type === 'tool' ? 'wrench' : 'layers'} data-testid="slots-form-icon" />
        </div>
      </div>
    </div>
  )
}

// SlotsEditor edits the slots of ui.json: the page tiles, tool buttons and
// commands of the chat header, the sidebar footer and the composer toolbar. A
// save sends the slots only, merged item by item with the file as it is then,
// so a change the assistant made while the tab was open is not lost; the other
// keys of ui.json are never sent.
export default function SlotsEditor() {
  const { t } = useTranslation('workspace')
  const { hasFeature } = useAuth()
  const wk = useWellKnown('ui')
  const { doc } = wk
  const pagesOn = hasFeature('chats') && hasFeature('pages')
  const { pages: own } = usePages(pagesOn)
  const { pages: shared } = useSharedPages(pagesOn)
  const [slots, setSlots] = useState(null)
  const [extra, setExtra] = useState({})
  const [selected, setSelected] = useState('chatHeader')
  const [openRow, setOpenRow] = useState(null)
  const [adding, setAdding] = useState(false)
  const [dirty, setDirty] = useState(false)
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState(null)
  const [savedAt, setSavedAt] = useState(false)
  const [mode, setMode] = useState('form')
  const [source, setSource] = useState('')
  const base = useRef({ data: {}, version: 0 })

  const take = (data) => {
    const read = readSlots(data)
    const out = {}
    for (const n of SLOT_NAMES) out[n] = read[n].map(withUid)
    setSlots(out)
    setExtra(data?.slots && typeof data.slots === 'object' ? data.slots : {})
  }

  useEffect(() => {
    if (!doc || dirty) return
    base.current = { data: doc.data || {}, version: doc.version ?? 0 }
    take(doc.data)
    setSource(sourceText(doc.data))
  }, [doc, dirty])

  const pageList = useMemo(() => {
    const out = []
    for (const p of own || []) out.push({ key: p.name, ref: { name: p.name, folder: 0 }, title: p.title || p.name, page: p })
    for (const p of shared || []) if (Number.isInteger(p.folder_id) && p.folder_id > 0) out.push({ key: `${p.folder_id}/${p.name}`, ref: { name: p.name, folder: p.folder_id }, title: p.title || p.name, page: p })
    return out
  }, [own, shared])
  const pageOf = (it) => pageList.find((p) => p.key === refKey({ name: it.page, folder: it.folder || 0 }))

  if (!slots) {
    return <section className="wk-card" data-testid="wk-card-slots"><div className="wk-loading"><LoadingSpinner size="sm" /></div></section>
  }

  const list = slots[selected]
  const edit = (name, next) => {
    setSlots((s) => ({ ...s, [name]: next.map(withUid) }))
    setDirty(true)
    setProblem(null)
    setSavedAt(false)
  }
  const addItem = (type) => {
    const it = withUid({ type })
    edit(selected, [...list, it])
    setOpenRow(it._uid)
    setAdding(false)
  }
  const describe = (it) => {
    if (it.type === 'tool') return t('wellKnown.slots.kind.tool')
    if (it.type === 'command') return t('wellKnown.slots.kind.command')
    const p = pageOf(it)
    if (!p) return t('wellKnown.slots.kind.pageMissing')
    return t(compactOf(p.page) ? 'wellKnown.slots.kind.pageCompact' : 'wellKnown.slots.kind.pageButton')
  }
  const counts = Object.fromEntries(SLOT_NAMES.map((n) => [n, t('wellKnown.slots.count', { count: slots[n].length, max: SLOT_CAPS[n] })]))
  const incomplete = SLOT_NAMES.some((n) => slots[n].some((it) => !slotItemOk(stripUid([it])[0])))

  const doSave = async () => {
    setBusy(true)
    setProblem(null)
    try {
      let d
      if (mode === 'source') {
        const parsed = parseSource(source)
        if (parsed.error) { setProblem({ message: t(`wellKnown.source.${parsed.error}`), errors: parsed.detail ? [parsed.detail] : [] }); return }
        d = await wk.save(parsed.value, { replace: true, version: base.current.version })
      } else {
        const mine = {}
        for (const n of SLOT_NAMES) mine[n] = stripUid(slots[n])
        let version = base.current.version
        let theirs = null
        for (let i = 0; ; i++) {
          const next = theirs ? cleanSlots(mergeSlots(base.current.data?.slots, mine, theirs.data?.slots), theirs.data?.slots) : cleanSlots(mine, extra)
          try {
            d = await wk.save({ slots: Object.keys(next).length ? next : null }, { version })
            break
          } catch (e) {
            if (!e.conflict || i >= MAX_TRIES) throw e
            theirs = e.current
            version = e.current.version ?? 0
          }
        }
      }
      setDirty(false)
      setSavedAt(true)
      base.current = { data: d.data || {}, version: d.version ?? 0 }
      take(d.data)
      setSource(sourceText(d.data))
    } catch (e) {
      setProblem(e.conflict ? { message: t('wellKnown.conflict'), errors: [] } : wellKnownProblem(e, t))
    } finally { setBusy(false) }
  }
  const discard = () => { setDirty(false); setProblem(null); setOpenRow(null); wk.reload() }
  const revert = async () => {
    setBusy(true)
    try {
      const d = await wk.revert()
      setDirty(false)
      base.current = { data: d.data || {}, version: d.version ?? 0 }
    } catch (e) { setProblem(wellKnownProblem(e, t)) } finally { setBusy(false) }
  }

  return (
    <section className="wk-card sl" aria-labelledby="wk-slots-title" data-testid="wk-card-slots">
      <header className="wk-card__head">
        <div className="wk-card__titles">
          <h2 id="wk-slots-title">{t('wellKnown.slots.title')}</h2>
          <p>{t('wellKnown.slots.lede')}</p>
        </div>
      </header>

      {doc && !doc.valid && (
        <div className="wk-note wk-note--warn" role="status" data-testid="wk-invalid-slots">
          <Icon name="warning" />
          <div><p>{doc.fallback ? t('wellKnown.invalidFallback') : t('wellKnown.invalid')}</p><ProblemDetails errors={doc.errors} /></div>
        </div>
      )}

      <div className="sl__body">
        <div className="sl__side">
          <div className="nv__side-title"><Icon name="eye" /> {t('wellKnown.slots.where')}</div>
          <SlotMap selected={selected} onSelect={(n) => { setSelected(n); setOpenRow(null); setAdding(false) }} counts={counts} />
          <ul className="sl__list" role="tablist" aria-label={t('wellKnown.slots.title')}>
            {SLOT_NAMES.map((n) => (
              <li key={n} role="none">
                <button
                  type="button" role="tab" aria-selected={selected === n} className="sl__slot"
                  onClick={() => { setSelected(n); setOpenRow(null); setAdding(false) }} data-testid={`slots-pick-${n}`}
                >
                  <Icon name={SLOT_ICONS[n]} />
                  <strong>{t(`wellKnown.slots.names.${n}`)}</strong>
                  <span>{counts[n]}</span>
                  {selected === n && <span className="sl__editing">{t('wellKnown.slots.editing')}</span>}
                </button>
              </li>
            ))}
          </ul>
        </div>

        <div className="sl__main">
          {mode === 'form' ? (
            <>
              <div className="sl__head">
                <h3>{t(`wellKnown.slots.names.${selected}`)}</h3>
                <span className="sl__cap" data-testid="slots-cap">{counts[selected]}</span>
              </div>
              <p className="sl__hint">{t(`wellKnown.slots.hint.${selected}`)}</p>
              <ol className="nv__rows" data-testid="slots-rows">
                {list.map((it, i) => {
                  const p = it.type === 'page' ? pageOf(it) : null
                  const name = itemLabel(it, p?.title) || t(`wellKnown.slots.add.${it.type}`)
                  const expanded = openRow === it._uid
                  return (
                    <li key={it._uid} className={`nv-row sl-row${expanded ? ' is-open' : ''}`} data-testid="slots-row" data-key={slotKey(it)} data-type={it.type}>
                      <div className="sl-row__line">
                        <Icon name={it.icon || (it.type === 'page' ? (p?.page?.icon || 'file-code') : it.type === 'tool' ? 'wrench' : 'code')} className="nv-row__icon" />
                        <button type="button" className="sl-row__name" aria-expanded={expanded} onClick={() => setOpenRow(expanded ? null : it._uid)} data-testid="slots-row-open">
                          <strong>{name}</strong>
                          <small>{describe(it)}</small>
                        </button>
                        <span className="nv-row__tools">
                          <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm dk-btn--icon" aria-label={t('wellKnown.nav.moveUp', { name })} disabled={i === 0} onClick={() => edit(selected, move(list, i, i - 1))} data-testid="slots-row-up"><Icon name="arrow-up" /></button>
                          <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm dk-btn--icon" aria-label={t('wellKnown.nav.moveDown', { name })} disabled={i === list.length - 1} onClick={() => edit(selected, move(list, i, i + 1))} data-testid="slots-row-down"><Icon name="arrow-down" /></button>
                          <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm dk-btn--icon" aria-label={t('wellKnown.nav.remove', { name })} title={t('wellKnown.nav.remove', { name })} onClick={() => edit(selected, list.filter((_, j) => j !== i))} data-testid="slots-row-remove"><Icon name="trash" /></button>
                        </span>
                      </div>
                      {expanded && <ItemForm item={it} pages={pageList} t={t} onChange={(next) => edit(selected, list.map((x, j) => (j === i ? next : x)))} />}
                    </li>
                  )
                })}
              </ol>
              {list.length === 0 && <p className="sl__empty" data-testid="slots-empty">{t('wellKnown.slots.empty')}</p>}

              <div className="nv__add">
                <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={!canAddTo(slots, selected)} aria-expanded={adding} onClick={() => setAdding((v) => !v)} data-testid="slots-add">
                  <Icon name="plus" /> {t('wellKnown.slots.addItem')} <Icon name="chevron-down" />
                </button>
                {!canAddTo(slots, selected) && <span className="dk-hint" data-testid="slots-full">{t('wellKnown.slots.full', { max: SLOT_CAPS[selected] })}</span>}
              </div>
              {adding && (
                <div className="nv-add sl-add" data-testid="slots-add-menu">
                  <ul className="nv-add__menu" role="menu">
                    {ADD_KINDS.map(([k, icon]) => (
                      <li key={k} role="none">
                        <button type="button" role="menuitem" className="nv-add__opt" onClick={() => addItem(k)} data-testid={`slots-add-${k}`}>
                          <span className="nv-add__chip"><Icon name={icon} /></span>
                          <span><strong>{t(`wellKnown.slots.add.${k}`)}</strong><small>{t(`wellKnown.slots.add.${k}Hint`)}</small></span>
                        </button>
                      </li>
                    ))}
                  </ul>
                  <button type="button" className="wk-linkbtn nv-add__close" onClick={() => setAdding(false)}>{t('wellKnown.cancel')}</button>
                </div>
              )}
            </>
          ) : (
            <div className="dk-field">
              <label className="dk-label" htmlFor="wk-slots-source">{t('wellKnown.source.label')}</label>
              <textarea id="wk-slots-source" className="dk-input dk-textarea dk-mono wk-source" spellCheck={false} value={source} data-testid="wk-source-text-slots" onChange={(e) => { setSource(e.target.value); setDirty(true); setProblem(null) }} />
              <p className="dk-hint">{t('wellKnown.source.hint')}</p>
            </div>
          )}

          {problem && (
            <div className="wk-note wk-note--error" role="alert" data-testid="wk-problem-slots">
              <Icon name="alert-circle" />
              <div><p>{problem.message}</p><ProblemDetails errors={problem.errors} /></div>
            </div>
          )}
          {incomplete && mode === 'form' && <p className="wk-note wk-note--info" data-testid="slots-incomplete"><Icon name="info" /> {t('wellKnown.slots.incomplete')}</p>}
          <div className="wk-actions">
            <button type="button" className="dk-btn dk-btn--primary" disabled={!dirty || busy || (incomplete && mode === 'form')} onClick={doSave} data-testid="wk-save-slots">
              {busy ? <LoadingSpinner size="sm" /> : <Icon name="save" />} {t('wellKnown.save')}
            </button>
            {dirty && <button type="button" className="dk-btn dk-btn--ghost" disabled={busy} onClick={discard} data-testid="slots-discard">{t('wellKnown.nav.discard')}</button>}
            {savedAt && !dirty && <span className="wk-status" role="status">{t('wellKnown.nav.saved')}</span>}
          </div>
        </div>
      </div>

      <FileFooter
        name="slots" file={WELL_KNOWN_FILES.ui} onRevert={revert} canRevert={!busy && !!doc?.exists}
        sourceLabel={mode === 'form' ? t('wellKnown.source.label') : t('wellKnown.form')}
        onSource={() => {
          if (mode === 'form') {
            const mine = {}
            for (const n of SLOT_NAMES) mine[n] = stripUid(slots[n])
            const next = cleanSlots(mine, extra)
            setSource(sourceText(dirty ? { ...base.current.data, slots: next } : base.current.data))
            setMode('source')
          } else setMode('form')
        }}
      />
    </section>
  )
}
