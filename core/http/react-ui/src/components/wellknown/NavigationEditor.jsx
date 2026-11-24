/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useRef, useState } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import LoadingSpinner from '../LoadingSpinner'
import { FileFooter, ProblemDetails } from './WellKnownCard'
import { useWellKnown } from '../../hooks/useWellKnown'
import { useNavLinks } from '../../hooks/useNavConfig'
import { usePages } from '../../hooks/usePages'
import { useSharedPages } from '../../hooks/useSharedPages'
import { useNavigation } from '../../context/NavigationContext'
import { useAuth } from '../../context/AuthContext'
import { chatsApi } from '../../utils/api'
import { navBuiltins, primaryDestinations, visiblePrimary } from '../../utils/navigationModel'
import {
  NAV_ALWAYS_SHOWN, NAV_LIMITS, canAdd, cleanRows, editorRows, defaultRows, itemKey, itemKind, mergeNav, rowKind, navUrlOk,
  readItems, rename, sections, setHidden, withGroup, withLink,
} from '../../utils/nav'
import { move, parseSource, sourceText, wellKnownProblem, WELL_KNOWN_FILES } from '../../utils/wellKnown'
import './wellKnown.css'
import './navEditor.css'

let uid = 0
// Each row has an id of its own, so a row keeps its place in the page (and its
// focus) while its title changes. cleanRows leaves it out of the file.
const withUids = (rows) => rows.map(r => (r._uid ? r : { ...r, _uid: ++uid }))

const BUILTIN = new Map(navBuiltins.map(b => [b.id, b]))
const MAX_TRIES = 3

// The sidebar as the Navigation editor draws it: the same sections the real
// sidebar draws, with the fixed entries around them.
function Preview({ rows, available, links }) {
  const { t } = useTranslation('nav')
  const secs = sections(rows.filter(r => itemKind(r) !== ''), { available, linkStatus: links.status })
  const name = (e) => e.label || t(BUILTIN.get(e.id).labelKey)
  return (
    <aside className="nv-prev" aria-label={t('wellKnown.nav.preview', { ns: 'workspace' })} data-testid="nav-preview">
      <div className="nv-prev__new"><Icon name="plus" /> {t('newChat')}</div>
      <div className="nv-prev__scroll">
        {secs.map(sec => (
          <div key={sec.key} className="nv-prev__sec">
            {sec.group && <div className="nv-prev__group" data-testid="nav-preview-group">{sec.group}</div>}
            {sec.entries.map(e => (
              <div key={e.key} className="nv-prev__item" data-testid="nav-preview-item">
                <Icon name={e.kind === 'builtin' ? BUILTIN.get(e.id).icon : links.icon(e.item)} />
                <span>{e.kind === 'builtin' ? name(e) : (links.title(e.item) || t('untitledChat'))}</span>
                {e.kind === 'link' && e.item.type === 'url' && <Icon name="external-link" className="nv-prev__ext" />}
              </div>
            ))}
          </div>
        ))}
        <div className="nv-prev__sec nv-prev__fixed">
          <div className="nv-prev__group">{t('recentChats')}</div>
          <div className="nv-prev__item nv-prev__item--sub">{t('items.allChats')}</div>
        </div>
      </div>
      <div className="nv-prev__foot">
        <div className="nv-prev__item"><Icon name="sliders" /><span>{t('sections.operate')}</span></div>
        <div className="nv-prev__item"><Icon name="user-settings" /><span>{t('yourSettings')}</span></div>
      </div>
    </aside>
  )
}

// The panel under "Add link": a page, a chat or an address.
function AddLink({ rows, onAdd, onClose, pagesOn }) {
  const { t } = useTranslation('workspace')
  const { pages } = usePages(pagesOn)
  const { pages: shared } = useSharedPages(pagesOn)
  const [kind, setKind] = useState(null)
  const [chats, setChats] = useState(null)
  const [pick, setPick] = useState('')
  const [label, setLabel] = useState('')
  const [url, setUrl] = useState('')
  const own = pages.map(p => ({ id: p.name, name: p.title || p.name }))
  const options = kind === 'page' ? own : (chats || [])

  useEffect(() => {
    if (kind !== 'chat' || chats) return
    chatsApi.list(50, 'chat', 'person')
      .then(r => setChats((r?.conversations || []).filter(c => c.kind !== 'subagent').map(c => ({ id: c.id, name: c.name || c.title || c.id }))))
      .catch(() => setChats([]))
  }, [kind, chats])

  const choose = (k) => { setKind(k); setPick(''); setLabel(''); setUrl('') }
  const urlBad = url !== '' && !navUrlOk(url.trim())
  const ready = kind === 'url' ? navUrlOk(url.trim()) : pick !== ''
  const add = () => {
    if (!ready) return
    const link = kind === 'url' ? { type: 'url', url: url.trim() } : { type: kind, [kind]: pick }
    const text = label.trim() || (kind === 'url' ? '' : options.find(o => o.id === pick)?.name || '')
    if (text) link.label = text.slice(0, NAV_LIMITS.label)
    onAdd(link)
  }

  const kinds = [['page', 'file-code'], ['chat', 'chat'], ['url', 'globe']]
  return (
    <div className="nv-add" data-testid="nav-add-panel">
      {!kind ? (
        <ul className="nv-add__menu" role="menu">
          {kinds.map(([k, icon]) => (
            <li key={k} role="none">
              <button type="button" role="menuitem" className="nv-add__opt" onClick={() => choose(k)} data-testid={`nav-add-${k}`}>
                <span className="nv-add__chip"><Icon name={icon} /></span>
                <span><strong>{t(`wellKnown.nav.add.${k}`)}</strong><small>{t(`wellKnown.nav.add.${k}Hint`)}</small></span>
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <div className="nv-add__form">
          {kind === 'url' ? (
            <>
              <label className="dk-label" htmlFor="nv-url">{t('wellKnown.nav.add.address')}</label>
              <input id="nv-url" className="dk-input" value={url} placeholder="https://" maxLength={2048} onChange={e => setUrl(e.target.value)} aria-invalid={urlBad || undefined} data-testid="nav-add-url-input" />
              {urlBad && <p className="dk-hint nv-add__bad" role="alert">{t('wellKnown.nav.add.urlInvalid')}</p>}
            </>
          ) : (
            <>
              <label className="dk-label" htmlFor="nv-pick">{t(`wellKnown.nav.add.pick.${kind}`)}</label>
              <select id="nv-pick" className="dk-select" value={pick} onChange={e => setPick(e.target.value)} data-testid="nav-add-select">
                <option value="">{t(`wellKnown.nav.add.pick.${kind}`)}</option>
                {options.filter(o => !rows.some(r => itemKey(r) === `link:${kind}:${o.id}`)).map(o => <option key={o.id} value={o.id}>{o.name}</option>)}
              </select>
              {options.length === 0 && (kind === 'page' || chats) && <p className="dk-hint">{t(`wellKnown.nav.add.none.${kind}`)}</p>}
            </>
          )}
          <label className="dk-label" htmlFor="nv-label">{t('wellKnown.nav.add.name')}</label>
          <input id="nv-label" className="dk-input" value={label} maxLength={NAV_LIMITS.label} onChange={e => setLabel(e.target.value)} data-testid="nav-add-label" />
          <div className="wk-actions">
            <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" disabled={!ready} onClick={add} data-testid="nav-add-confirm">{t('wellKnown.nav.add.confirm')}</button>
            <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => setKind(null)}>{t('wellKnown.nav.add.back')}</button>
          </div>
        </div>
      )}
      <button type="button" className="wk-linkbtn nv-add__close" onClick={onClose}>{t('wellKnown.cancel')}</button>
    </div>
  )
}

// NavigationEditor edits nav.json: the order of the sidebar, the names of its
// entries, which ones are hidden, groups and links of your own. The rows are the
// file's items; a save sends the whole list, merged by item with the file as it
// is now, so a change the assistant made while this tab was open is not lost.
// New chat, Chats, the settings entries and the search are not rows: the file
// cannot touch them.
export default function NavigationEditor() {
  const { t } = useTranslation('workspace')
  const { t: tn } = useTranslation('nav')
  const ctx = useOutletContext() || {}
  const featureMap = ctx.features
  const { isAdmin, hasFeature } = useAuth()
  const { builtin } = useNavigation()
  const wk = useWellKnown('nav')
  const { doc } = wk
  const [rows, setRows] = useState(null)
  const [extras, setExtras] = useState([])
  const [dirty, setDirty] = useState(false)
  const [reset, setReset] = useState(false)
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState(null)
  const [mode, setMode] = useState('form')
  const [source, setSource] = useState('')
  const [adding, setAdding] = useState(false)
  const [savedAt, setSavedAt] = useState(false)
  const [dragging, setDragging] = useState(null)
  const [over, setOver] = useState(null)
  const base = useRef({ data: {}, version: 0 })
  const listRef = useRef(null)
  const focusRow = useRef(null)

  // Take the file whenever it changes and the rows hold no edit of their own.
  useEffect(() => {
    if (!doc || dirty) return
    base.current = { data: doc.data || {}, version: doc.version ?? 0 }
    const e = editorRows(readItems(doc.data))
    setRows(withUids(e.rows))
    setExtras(e.extras)
    setSource(sourceText(doc.data))
  }, [doc, dirty])

  useEffect(() => {
    if (focusRow.current === null) return
    const el = listRef.current?.querySelectorAll('[data-testid="nav-row-name"]')[focusRow.current]
    focusRow.current = null
    el?.focus()
    el?.select?.()
  }, [rows])

  const pagesOn = hasFeature('chats') && hasFeature('pages')
  const { pages } = usePages(pagesOn)
  const { pages: shared } = useSharedPages(pagesOn)
  const hasPages = pagesOn && (pages.length > 0 || shared.length > 0)
  const available = useMemo(() => {
    const auth = { isAdmin, hasFeature, hasBuiltin: builtin.length > 0, features: featureMap || {}, hasPages }
    return new Set(['home', ...visiblePrimary(primaryDestinations, auth).map(i => i.id)])
  }, [isAdmin, hasFeature, builtin, featureMap, hasPages])
  const links = useNavLinks(rows || [], { pagesOn, enabled: !!rows })

  const edit = (next) => { setRows(withUids(next)); setDirty(true); setReset(false); setProblem(null); setSavedAt(false) }
  const moveRow = (i, j) => { if (j >= 0 && j < rows.length) edit(move(rows, i, j)) }

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
        const mine = cleanRows(rows, extras)
        let version = base.current.version
        let theirs = null
        for (let i = 0; ; i++) {
          // After a conflict the edit goes on top of the newer file, item by
          // item. A reset is the person's answer for the whole list.
          const items = theirs && !reset ? mergeNav(base.current.data, rows, extras, theirs.data) : mine
          try {
            d = await wk.save({ items }, { version })
            break
          } catch (e) {
            if (!e.conflict || i >= MAX_TRIES) throw e
            theirs = e.current
            version = e.current.version ?? 0
          }
        }
      }
      setDirty(false)
      setReset(false)
      setSavedAt(true)
      // The next read takes the file as saved.
      base.current = { data: d.data || {}, version: d.version ?? 0 }
      const e = editorRows(readItems(d.data))
      setRows(withUids(e.rows))
      setExtras(e.extras)
      setSource(sourceText(d.data))
    } catch (e) {
      setProblem(e.conflict ? { message: t('wellKnown.conflict'), errors: [] } : wellKnownProblem(e, t))
    } finally { setBusy(false) }
  }

  const discard = () => { setDirty(false); setReset(false); setProblem(null); wk.reload() }
  const resetToDefault = () => { setRows(withUids(defaultRows())); setExtras([]); setDirty(true); setReset(true); setProblem(null); setSavedAt(false) }
  const revert = async () => {
    setBusy(true)
    try {
      const d = await wk.revert()
      setDirty(false)
      base.current = { data: d.data || {}, version: d.version ?? 0 }
    } catch (e) { setProblem(wellKnownProblem(e, t)) } finally { setBusy(false) }
  }

  if (!rows) {
    return <section className="wk-card" data-testid="wk-card-nav"><div className="wk-loading"><LoadingSpinner size="sm" /></div></section>
  }

  const onDrop = (i) => {
    if (dragging !== null && dragging !== i) moveRow(dragging, i)
    setDragging(null)
    setOver(null)
  }
  const nameOf = (r) => {
    switch (rowKind(r)) {
      case 'group': return r.group
      case 'builtin': return r.label || tn(BUILTIN.get(r.id).labelKey)
      default: return links.title(r) || tn('untitledChat')
    }
  }

  return (
    <section className="wk-card nv" aria-labelledby="wk-nav-title" data-testid="wk-card-nav">
      <header className="wk-card__head">
        <div className="wk-card__titles">
          <h2 id="wk-nav-title">{t('wellKnown.nav.title')}</h2>
          <p>{t('wellKnown.nav.lede')}</p>
        </div>
      </header>

      {doc && !doc.valid && (
        <div className="wk-note wk-note--warn" role="status" data-testid="wk-invalid-nav">
          <Icon name="warning" />
          <div>
            <p>{doc.fallback ? t('wellKnown.invalidFallback') : t('wellKnown.invalid')}</p>
            <ProblemDetails errors={doc.errors} />
          </div>
        </div>
      )}
      {wk.error && !doc && (
        <div className="wk-note wk-note--error" role="alert"><Icon name="alert-circle" /><div><p>{t('wellKnown.loadFailed')}</p><ProblemDetails errors={[wk.error.message]} /></div></div>
      )}

      <div className="nv__body">
        <div className="nv__main">
          {mode === 'form' ? (
            <>
              <ol className="nv__rows" ref={listRef} data-testid="nav-rows">
                {rows.map((r, i) => {
                  const kind = rowKind(r)
                  const name = nameOf(r)
                  const missing = kind === 'link' && r.type !== 'url' && links.status(r) === 'missing'
                  const info = kind === 'builtin' ? BUILTIN.get(r.id) : null
                  const unavailable = kind === 'builtin' && !available.has(r.id)
                  const fixed = kind === 'builtin' && NAV_ALWAYS_SHOWN.includes(r.id)
                  const hidden = kind === 'builtin' && r.hidden && !fixed
                  const original = kind === 'builtin' ? tn(info.labelKey) : ''
                  return (
                    <li
                      key={r._uid}
                      className={`nv-row nv-row--${kind}${hidden ? ' is-hidden' : ''}${missing || unavailable ? ' is-dim' : ''}${dragging === i ? ' is-dragging' : ''}${over === i && dragging !== i ? ' is-over' : ''}`}
                      draggable={false}
                      onDragOver={e => { if (dragging !== null) { e.preventDefault(); setOver(i) } }}
                      onDrop={e => { e.preventDefault(); onDrop(i) }}
                      data-testid="nav-row" data-key={itemKey(r)} data-kind={kind}
                    >
                      <span
                        className="nv-row__grip" role="button" tabIndex={0} draggable
                        aria-label={t('wellKnown.nav.drag', { name })}
                        onDragStart={e => { setDragging(i); e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/plain', itemKey(r)) }}
                        onDragEnd={() => { setDragging(null); setOver(null) }}
                        onKeyDown={e => {
                          if (e.key === 'ArrowUp') { e.preventDefault(); moveRow(i, i - 1); focusRow.current = null; requestAnimationFrame(() => listRef.current?.children[i - 1]?.querySelector('.nv-row__grip')?.focus()) }
                          else if (e.key === 'ArrowDown') { e.preventDefault(); moveRow(i, i + 1); requestAnimationFrame(() => listRef.current?.children[i + 1]?.querySelector('.nv-row__grip')?.focus()) }
                        }}
                        data-testid="nav-row-grip"
                      />
                      <Icon name={kind === 'group' ? 'chevron-down' : kind === 'builtin' ? info.icon : links.icon(r)} className="nv-row__icon" />
                      <input
                        className="nv-row__name" value={kind === 'group' ? r.group : (r.label ?? name)} maxLength={NAV_LIMITS.label}
                        aria-label={t('wellKnown.nav.name', { name: kind === 'group' ? r.group : name })}
                        onChange={e => edit(rename(rows, i, e.target.value))}
                        onBlur={e => { if (kind !== 'group' && !e.target.value.trim() && r.label !== undefined) edit(rename(rows, i, '')) }}
                        data-testid="nav-row-name"
                      />
                      {kind === 'builtin' && r.label && <span className="nv-row__was">{t('wellKnown.nav.was', { name: original })}</span>}
                      <span className="nv-row__tag">
                        {kind === 'builtin' && t('wellKnown.nav.tag.builtin')}
                        {kind === 'group' && t('wellKnown.nav.tag.group')}
                        {kind === 'link' && t(`wellKnown.nav.tag.${r.type}`)}
                      </span>
                      {missing && <span className="nv-row__badge" data-testid="nav-row-missing" title={t('wellKnown.nav.missingHint', { kind: t(`wellKnown.nav.tag.${r.type}`) })}>{t('wellKnown.nav.missing')}</span>}
                      {unavailable && <span className="nv-row__badge" title={t('wellKnown.nav.unavailableHint')}>{t('wellKnown.nav.unavailable')}</span>}
                      <span className="nv-row__tools">
                        {kind === 'builtin' && (fixed
                          ? <span className="nv-row__lock" title={t('wellKnown.nav.homeFixed')} role="img" aria-label={t('wellKnown.nav.homeFixed')}><Icon name="lock" /></span>
                          : (
                            <button
                              type="button" className="dk-btn dk-btn--ghost dk-btn--sm dk-btn--icon" data-testid="nav-row-hide" aria-pressed={!!r.hidden}
                              aria-label={t(r.hidden ? 'wellKnown.nav.show' : 'wellKnown.nav.hide', { name })} title={t(r.hidden ? 'wellKnown.nav.show' : 'wellKnown.nav.hide', { name })}
                              onClick={() => edit(setHidden(rows, i, !r.hidden))}
                            ><Icon name={r.hidden ? 'eye-off' : 'eye'} /></button>
                          ))}
                        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm dk-btn--icon" aria-label={t('wellKnown.nav.moveUp', { name })} disabled={i === 0} onClick={() => moveRow(i, i - 1)} data-testid="nav-row-up"><Icon name="arrow-up" /></button>
                        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm dk-btn--icon" aria-label={t('wellKnown.nav.moveDown', { name })} disabled={i === rows.length - 1} onClick={() => moveRow(i, i + 1)} data-testid="nav-row-down"><Icon name="arrow-down" /></button>
                        {kind !== 'builtin' && (
                          <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm dk-btn--icon" aria-label={t('wellKnown.nav.remove', { name })} title={t('wellKnown.nav.remove', { name })} onClick={() => edit(rows.filter((_, j) => j !== i))} data-testid="nav-row-remove"><Icon name="trash" /></button>
                        )}
                      </span>
                    </li>
                  )
                })}
              </ol>

              <div className="nv__add">
                <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={!canAdd(rows, 'link')} aria-expanded={adding} onClick={() => setAdding(v => !v)} data-testid="nav-add-link">
                  {t('wellKnown.nav.addLink')} <Icon name="chevron-down" />
                </button>
                <button
                  type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={!canAdd(rows, 'group')} data-testid="nav-add-group"
                  onClick={() => { const next = withGroup(rows, t('wellKnown.nav.newGroup')); focusRow.current = next.length - 1; edit(next) }}
                ><Icon name="plus" /> {t('wellKnown.nav.addGroup')}</button>
                <span className="nv__fixed"><Icon name="lock" /> {t('wellKnown.nav.fixed')}</span>
              </div>
              {adding && (
                <AddLink
                  rows={rows} pagesOn={pagesOn} onClose={() => setAdding(false)}
                  onAdd={(link) => { edit(withLink(rows, link)); setAdding(false) }}
                />
              )}
            </>
          ) : (
            <div className="dk-field">
              <label className="dk-label" htmlFor="wk-nav-source">{t('wellKnown.source.label')}</label>
              <textarea
                id="wk-nav-source" className="dk-input dk-textarea dk-mono wk-source" spellCheck={false} value={source}
                data-testid="wk-source-text-nav"
                onChange={e => { setSource(e.target.value); setDirty(true); setProblem(null) }}
              />
              <p className="dk-hint">{t('wellKnown.source.hint')}</p>
            </div>
          )}

          {problem && (
            <div className="wk-note wk-note--error" role="alert" data-testid="wk-problem-nav">
              <Icon name="alert-circle" />
              <div><p>{problem.message}</p><ProblemDetails errors={problem.errors} /></div>
            </div>
          )}
          <div className="wk-actions">
            <button type="button" className="dk-btn dk-btn--primary" disabled={!dirty || busy} onClick={doSave} data-testid="wk-save-nav">
              {busy ? <LoadingSpinner size="sm" /> : <Icon name="save" />} {t('wellKnown.save')}
            </button>
            {dirty && <button type="button" className="dk-btn dk-btn--ghost" disabled={busy} onClick={discard} data-testid="nav-discard">{t('wellKnown.nav.discard')}</button>}
            {savedAt && !dirty && <span className="wk-status" role="status">{t('wellKnown.nav.saved')}</span>}
          </div>
        </div>

        <div className="nv__side">
          <div className="nv__side-title"><Icon name="eye" /> {t('wellKnown.nav.preview')}</div>
          <Preview rows={rows} available={available} links={links} />
        </div>
      </div>

      <FileFooter
        name="nav" file={WELL_KNOWN_FILES.nav} onRevert={revert} canRevert={!busy && !!doc?.exists}
        sourceLabel={mode === 'form' ? t('wellKnown.source.label') : t('wellKnown.form')}
        onSource={() => { if (mode === 'form') { setSource(dirty ? sourceText({ ...base.current.data, items: cleanRows(rows, extras) }) : sourceText(base.current.data)); setMode('source') } else setMode('form') }}
        extra={<button type="button" className="wk-linkbtn" onClick={resetToDefault} disabled={busy} data-testid="nav-reset">{t('wellKnown.nav.reset')}</button>}
      />
    </section>
  )
}
