/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import Icon from '../Icon'
import Skeleton from '../Skeleton'
import { ProblemDetails } from './WellKnownCard'
import { useWellKnown } from '../../hooks/useWellKnown'
import { filesApi } from '../../utils/api'
import { widgetList, widgetData, cleanWidgets, move, sparkPath, formatMetric, wellKnownProblem, itemLinkTarget, WIDGET_TYPES } from '../../utils/wellKnown'
import PageLinkAsk from '../PageLinkAsk'
import { usePageLinks } from '../../hooks/usePageLinks'
import { cadenceOf, footerState, linkedTrigger, scheduledCount } from '../../utils/widgetTrigger'
import { useHomeTriggers } from '../../hooks/useHomeTriggers'
import WidgetFooter from './WidgetFooter'
import PageTile from './PageTile'
import { usePages } from '../../hooks/usePages'
import { useSharedPages } from '../../hooks/useSharedPages'
import { MAX_PAGE_TILES, eligiblePages, refKey, tileRef } from '../../utils/pageTiles'
import { areaChoices, areaKey, areaTarget, areaValue } from '../../utils/pageArea'
import { CHATS_ROWS, LIST_ROWS, WIDGET_SIZES, contentUnits, unitsKey, widgetSize, widgetUnits } from '../../utils/homeGrid'
import { useGridColumns } from '../../hooks/useGridColumns'
import PackedGrid from './PackedGrid'
import Segmented from '../ui/Segmented'
import '../../pages/pages.css'
import './wellKnown.css'
import './pageArea.css'

function shortTime(ts, locale) {
  if (!ts) return ''
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ''
  const sameDay = new Date().toDateString() === d.toDateString()
  try {
    return sameDay
      ? d.toLocaleTimeString(locale, { hour: 'numeric', minute: '2-digit' })
      : d.toLocaleDateString(locale, { weekday: 'short' })
  } catch { return '' }
}

// DataWidget draws a list or a metric from its data file. A file that is
// missing, broken or unreadable shows a line that says so; the page never breaks.
// A widget that a trigger feeds says when it runs, and its footer pauses,
// resumes or retries the trigger. onUnits(key, units) gets the height of what
// it shows in row units, for the grid.
function DataWidget({ w, triggers, onUnits }) {
  const { t } = useTranslation('workspace')
  const { t: tp } = useTranslation('pages')
  // An external item link asks first, like a link of a page: the data file is
  // written by trigger runs and the assistant, which read web text.
  const links = usePageLinks()
  const wk = useWellKnown('widget-data', { file: w.source, enabled: !!w.source })
  const d = useMemo(() => (wk.error ? { state: 'error', errors: [wk.error.message], items: [], series: [], value: null } : widgetData(wk.doc)), [wk.doc, wk.error])
  const left = d.items?.filter(it => !it.done).length || 0
  const { trigger, proposal } = linkedTrigger(w, triggers.triggers, triggers.proposals)
  const state = d.state === 'loading' || !w.source ? '' : footerState({ data: d, trigger, proposal })
  const footId = `wk-foot-${w.id}`
  // What the body shows, for its height: a line (no source, loading, empty or
  // broken), a metric, or the rows of a list.
  let shows = { kind: 'list', rows: 0 }

  let body
  if (!w.source) body = <p className="wk-widget__empty">{t('wellKnown.home.noSource')}</p>
  else if (d.state === 'loading') body = <Skeleton variant="line" width="80%" />
  else if (d.state === 'empty' || (d.state === 'error' && !d.items.length && d.value === null)) {
    body = <p className="wk-widget__empty">{d.state === 'empty' ? t('wellKnown.home.footer.noData') : t('wellKnown.home.broken')}</p>
  } else if (w.type === 'metric') {
    const path = sparkPath(d.series, 240, 56)
    shows = { kind: 'metric', spark: !!path }
    body = (
      <div className="wk-metric">
        <span className="wk-metric__v">{formatMetric(d.value)}</span>
        {d.note && <span className="wk-metric__n">{d.note}</span>}
        {path && (
          <svg className="wk-spark" viewBox="0 0 240 56" preserveAspectRatio="none" aria-hidden="true">
            <path d={`${path} L240,56 L0,56 Z`} />
            <path d={path} />
          </svg>
        )}
      </div>
    )
  } else if (d.items.length === 0) {
    body = <p className="wk-widget__empty">{t('wellKnown.home.nothing')}</p>
  } else {
    shows = { kind: 'list', rows: d.items.length }
    body = (
      <ul className="wk-wlist">
        {d.items.slice(0, LIST_ROWS).map((it, i) => (
          <li key={i} className={it.done ? 'is-done' : undefined}>
            <span className={`wk-box${it.done ? ' wk-box--done' : ''}`} aria-hidden="true">{it.done && <Icon name="check" />}</span>
            {itemLinkTarget(it.link) ? (
              <button
                type="button" className="wk-t wk-link" data-href={it.link.href} title={t('wellKnown.home.opensSite', { host: it.link.host })} data-testid="widget-item-link"
                onClick={() => { links.onLink(itemLinkTarget(it.link)) }}
              >
                <span className="wk-link__text">{it.label}</span><Icon name="external-link" className="wk-link__ext" />
              </button>
            ) : it.link && !it.link.external ? (
              <Link className="wk-t wk-link" to={it.link.to} title={it.label} data-testid="widget-item-link">
                <span className="wk-link__text">{it.label}</span>
              </Link>
            ) : (
              <span className="wk-t" title={it.label}>{it.label}</span>
            )}
            {it.meta && <span className="wk-m">{it.meta}</span>}
          </li>
        ))}
      </ul>
    )
  }

  const units = contentUnits(shows.kind, { ...shows, footer: !!state })
  // Before the paint, so the grid never draws the tile at another height first.
  useLayoutEffect(() => { onUnits?.(unitsKey(w), units) }, [w.id, w.type, w.source, units, onUnits]) // eslint-disable-line react-hooks/exhaustive-deps

  const cadence = cadenceOf(trigger)
  let aside = ''
  if (cadence) aside = <span className="wk-widget__aside wk-widget__tag"><Icon name={cadence === 'paused' ? 'pause' : 'clock'} /> {t(`wellKnown.home.footer.cadence.${cadence}`)}</span>
  else if (w.type === 'metric') aside = <span className="wk-widget__aside">{t('wellKnown.home.types.metric')}</span>
  else if ((d.state === 'ok' || d.state === 'stale') && d.items.length) aside = <span className="wk-widget__aside">{t('wellKnown.home.left', { count: left })}</span>
  const dim = state === 'stale' || state === 'invalid'
  return (
    <section
      className={`wk-widget${dim ? ' wk-widget--dim' : ''}`} data-testid={`home-widget-${w.id}`} data-state={d.state}
      aria-label={w.title} aria-describedby={state ? footId : undefined}
    >
      <h2><span>{w.title}</span>{aside}</h2>
      <div className="wk-widget__body">{body}</div>
      {links.link && (
        <div className="wk-widget__ask">
          <PageLinkAsk key={links.link.id} link={links.link} t={tp} label={t('wellKnown.home.linkAsk')} onOpen={links.open} onCancel={links.cancel} />
        </div>
      )}
      {state && (
        <WidgetFooter
          id={footId} state={state} w={w} d={d} trigger={trigger} proposal={proposal} act={triggers.act}
          onRevert={wk.doc?.fallback ? () => wk.revert().catch(() => {}) : null}
        />
      )}
    </section>
  )
}

function ChatsWidget({ w, conversations, onResume }) {
  const { t, i18n } = useTranslation('workspace')
  return (
    <section className="wk-widget" data-testid={`home-widget-${w.id}`} data-state="ok" aria-label={w.title}>
      <h2><span>{w.title}</span><span className="wk-widget__aside">{t('wellKnown.home.fromChats')}</span></h2>
      {conversations.length === 0
        ? <p className="wk-widget__empty">{t('wellKnown.home.noChats')}</p>
        : (
          <ul className="wk-wlist">
            {conversations.slice(0, CHATS_ROWS).map(c => (
              <li key={c.id}>
                <button type="button" onClick={() => onResume(c)}>
                  <Icon name="chat" />
                  <span className="wk-t" title={c.title}>{c.title}</span>
                  <span className="wk-m">{shortTime(c.updatedAt, i18n.language)}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
    </section>
  )
}

// PagePick chooses the page of a page widget among the pages with a widget
// block. A page the widget names that is no longer eligible stays in the list,
// marked, so a save does not drop it.
function PagePick({ r, eligible, byKey, onPick, t }) {
  const key = refKey(tileRef(r))
  const current = byKey.get(key)
  return (
    <span className="wk-wrow__page">
      <span className="dk-select-wrap">
        <select className="dk-select" aria-label={t('wellKnown.home.page.pick')} value={key} onChange={e => onPick(e.target.value)} data-testid="home-layout-page">
          {!current && <option value={key}>{t('wellKnown.home.page.unavailable', { name: r.page || '' })}</option>}
          {eligible.map(e => (
            <option key={e.key} value={e.key}>
              {e.page.title || e.page.name}{e.ref.folder ? ` (${t('wellKnown.home.page.sharedLabel')})` : ''}
            </option>
          ))}
        </select>
      </span>
    </span>
  )
}

// LayoutEditor edits home.json: add, remove and reorder widgets, and pick the
// data file of each. The data files offered are the .json files of the home
// and of its data folder; any other path can be typed.
function LayoutEditor({ wk, onDone, cols, eligible, byKey, areaPages, areaOn, unitsOf }) {
  const { t } = useTranslation('workspace')
  const [rows, setRows] = useState(() => widgetList(wk.doc))
  const sizeOf = (r) => widgetSize(r, byKey.get(refKey(tileRef(r)))?.page)
  // The page area: the pageArea of the file as it is, kept as it was unless
  // the person picks another page.
  const areaWas = wk.doc?.data?.pageArea
  const [area, setArea] = useState(areaWas ?? null)
  const areaRef = areaTarget(area).ref
  const areaSel = areaKey(areaRef)
  const areaByKey = useMemo(() => new Map(areaPages.map(e => [e.key, e])), [areaPages])
  const pickArea = (key) => { setArea(key ? areaValue(areaByKey.get(key)?.ref) : null); setProblem(null) }
  const pageRows = rows.filter(r => r.type === 'page').length
  // The version the rows came from: a save starts from it, so a change by
  // someone else while editing is a conflict and not lost. After the conflict
  // is shown, a second Save puts these rows on top of the newer file.
  const baseVersion = useRef(wk.doc?.version ?? 0)
  const [files, setFiles] = useState([])
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState(null)

  useEffect(() => {
    let live = true
    const json = (list, prefix) => (list?.entries || []).filter(e => e.kind === 'file' && e.name.endsWith('.json')).map(e => prefix + e.name)
    Promise.all([filesApi.list('/').catch(() => null), filesApi.list('/data').catch(() => null)]).then(([root, data]) => {
      // The settings files are in LocalAI/; one still at the top of the home is
      // one LocalAI is about to move there, not a data file.
      const known = new Set(['persona.json', 'user.json', 'shortcuts.json', 'home.json', 'ui.json'])
      if (live) setFiles([...json(data, 'data/'), ...json(root, '').filter(f => !known.has(f))])
    })
    return () => { live = false }
  }, [])

  const set = (i, k, v) => { setRows(rows.map((r, j) => (j === i ? { ...r, [k]: v } : r))); setProblem(null) }
  const add = () => {
    // No id yet: the save makes one from the title the user gives it.
    setRows([...rows, { id: '', type: 'list', title: t('wellKnown.home.newTitle'), source: '' }])
  }
  const used = new Set(rows.filter(r => r.type === 'page').map(r => refKey(tileRef(r))))
  const addPage = () => {
    const pick = eligible.find(e => !used.has(e.key)) || eligible[0]
    if (!pick) return
    setRows([...rows, { id: '', type: 'page', title: '', source: '', page: pick.ref.name, ...(pick.ref.folder ? { folder: pick.ref.folder } : {}) }])
  }
  const setType = (i, type) => {
    setRows(rows.map((r, j) => {
      if (j !== i) return r
      if (type !== 'page') return { ...r, type }
      const pick = r.page ? null : eligible.find(e => !used.has(e.key)) || eligible[0]
      return { ...r, type, ...(pick ? { page: pick.ref.name, folder: pick.ref.folder || undefined } : {}) }
    }))
    setProblem(null)
  }
  const setPage = (i, key) => {
    const e = byKey.get(key)
    setRows(rows.map((r, j) => (j === i && e ? { ...r, page: e.ref.name, folder: e.ref.folder || undefined } : r)))
    setProblem(null)
  }
  const save = async () => {
    setBusy(true)
    try {
      const changed = JSON.stringify(area ?? null) !== JSON.stringify(areaWas ?? null)
      await wk.save({ widgets: cleanWidgets(rows), ...(changed ? { pageArea: area ?? null } : {}) }, { version: baseVersion.current })
      onDone()
    } catch (e) {
      setProblem(e.conflict ? { message: t('wellKnown.conflict'), errors: [] } : wellKnownProblem(e, t))
      if (e.conflict) {
        baseVersion.current = e.current?.version ?? baseVersion.current
        await wk.reload()
      }
    } finally { setBusy(false) }
  }

  return (
    <div className="wk-layout" data-testid="home-layout-editor">
      <h3>{t('wellKnown.home.editLayout')}</h3>
      <datalist id="wk-data-files">{files.map(f => <option key={f} value={f} />)}</datalist>
      <div className="wk-area__pick" data-testid="home-layout-area">
        <span className="wk-area__pick-label" id="wk-area-label">{t('wellKnown.home.area.label')}</span>
        <span className="dk-select-wrap">
          <select className="dk-select" aria-labelledby="wk-area-label" value={areaSel} onChange={e => pickArea(e.target.value)} data-testid="home-layout-area-pick">
            <option value="">{t('wellKnown.home.area.none')}</option>
            {areaSel && !areaByKey.has(areaSel) && <option value={areaSel}>{t('wellKnown.home.page.unavailable', { name: areaRef?.name || '' })}</option>}
            {areaPages.map(e => (
              <option key={e.key} value={e.key}>
                {e.page.title || e.page.name}{e.ref.folder ? ` (${t('wellKnown.home.page.sharedLabel')})` : ''}
              </option>
            ))}
          </select>
        </span>
        <span className="wk-note wk-area__pick-hint" data-testid="home-layout-area-hint">{areaOn === false ? t('wellKnown.home.area.pickOff') : t('wellKnown.home.area.pickHint')}</span>
      </div>
      {rows.length === 0 && <p className="wk-widget__empty">{t('wellKnown.home.noWidgets')}</p>}
      {rows.length > 0 && (
        <div className="wk-preview" data-testid="home-layout-preview">
          <p className="wk-note">{t('wellKnown.home.preview')}</p>
          <PackedGrid sizes={rows.map(sizeOf)} units={rows.map(unitsOf)} cols={cols} className="wk-widgets--preview" testId="home-layout-grid">
            {rows.map((r, i) => (
              <div key={i} className="wk-pv" data-testid="home-layout-block" data-size={sizeOf(r)}>
                <span className="wk-pv__name">{r.title || byKey.get(refKey(tileRef(r)))?.page?.title || t('wellKnown.home.title')}</span>
                <span className="wk-pv__size">{sizeOf(r).toUpperCase()}</span>
              </div>
            ))}
          </PackedGrid>
        </div>
      )}
      {rows.map((r, i) => (
        <div key={i} className="wk-wrow" data-testid="home-layout-row">
          <span className="dk-select-wrap">
            <select className="dk-select" aria-label={t('wellKnown.home.type')} value={r.type} onChange={e => setType(i, e.target.value)} data-testid="home-layout-type">
              {WIDGET_TYPES.map(ty => (
                <option key={ty} value={ty} disabled={ty === 'page' && r.type !== 'page' && (pageRows >= MAX_PAGE_TILES || eligible.length === 0)}>{t(`wellKnown.home.types.${ty}`)}</option>
              ))}
            </select>
          </span>
          <input
            className="dk-input" aria-label={t('wellKnown.home.title')} maxLength={60} value={r.title} onChange={e => set(i, 'title', e.target.value)}
            placeholder={r.type === 'page' ? (byKey.get(refKey(tileRef(r)))?.page?.widget?.title || byKey.get(refKey(tileRef(r)))?.page?.title || t('wellKnown.home.title')) : t('wellKnown.home.title')}
          />
          {r.type === 'page' ? (
            <PagePick r={r} eligible={eligible} byKey={byKey} onPick={key => setPage(i, key)} t={t} />
          ) : (
            <input
              className="dk-input dk-mono" list="wk-data-files" aria-label={t('wellKnown.home.source')} placeholder={r.type === 'chats' ? t('wellKnown.home.noSourceNeeded') : 'data/tasks.json'}
              value={r.type === 'chats' ? '' : r.source} disabled={r.type === 'chats'} onChange={e => set(i, 'source', e.target.value)}
            />
          )}
          <span className="wk-wrow__tools">
            <span className="wk-wrow__size">
              <Segmented
                label={t('wellKnown.home.size')} value={sizeOf(r)} onChange={v => set(i, 'size', v)}
                options={WIDGET_SIZES.map(z => ({ id: z, label: z.toUpperCase() }))} testId="home-layout-size" itemTestId="home-layout-size-"
              />
            </span>
            <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm dk-btn--icon" aria-label={t('wellKnown.moveUp')} disabled={i === 0} onClick={() => setRows(move(rows, i, i - 1))}><Icon name="arrow-up" /></button>
            <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm dk-btn--icon" aria-label={t('wellKnown.moveDown')} disabled={i === rows.length - 1} onClick={() => setRows(move(rows, i, i + 1))}><Icon name="arrow-down" /></button>
            <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm dk-btn--icon" aria-label={t('wellKnown.remove')} onClick={() => setRows(rows.filter((_, j) => j !== i))}><Icon name="trash" /></button>
          </span>
        </div>
      ))}
      {pageRows >= MAX_PAGE_TILES && <p className="wk-note" data-testid="home-layout-page-limit"><Icon name="info" /> {t('wellKnown.home.page.limit', { max: MAX_PAGE_TILES })}</p>}
      {eligible.length === 0 && <p className="wk-note" data-testid="home-layout-no-pages"><Icon name="info" /> {t('wellKnown.home.page.noneEligible')}</p>}
      {problem && <div className="wk-note wk-note--error" role="alert"><Icon name="alert-circle" /><div><p>{problem.message}</p><ProblemDetails errors={problem.errors} /></div></div>}
      <div className="wk-actions">
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={add} disabled={rows.length >= 12} data-testid="home-layout-add"><Icon name="plus" /> {t('wellKnown.home.add')}</button>
        {eligible.length > 0 && (
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={addPage} disabled={rows.length >= 12 || pageRows >= MAX_PAGE_TILES} data-testid="home-layout-add-page"><Icon name="file-code" /> {t('wellKnown.home.page.add')}</button>
        )}
        <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" onClick={save} disabled={busy} data-testid="home-layout-save"><Icon name="save" /> {t('wellKnown.save')}</button>
        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={onDone}>{t('wellKnown.cancel')}</button>
      </div>
    </div>
  )
}

// HomeWidgets is the part of Home that home.json defines: its widgets in
// order, and the bar that edits the layout or asks the assistant to.
//
//   wk             useWellKnown('home')
//   conversations  for a chats widget (resumeItems)
//   onResume       opens a chat
//   onAsk          opens a chat with a prompt about the layout
//   editRequest    a number that, when it grows, opens Edit layout (the
//                  "Customise Home" link of the library line)
export default function HomeWidgets({ wk, conversations, onResume, onAsk, editRequest = 0 }) {
  const { t } = useTranslation('workspace')
  const [editing, setEditing] = useState(false)
  const own = usePages()
  const shared = useSharedPages()
  // The pages that can be a tile: the ones whose page.json has a widget block.
  const eligible = useMemo(() => eligiblePages(own.pages, shared.pages), [own.pages, shared.pages])
  const byKey = useMemo(() => new Map(eligible.map(e => [e.key, e])), [eligible])
  const areaPages = useMemo(() => areaChoices(own.pages, shared.pages), [own.pages, shared.pages])
  const listedPage = (w) => {
    const ref = tileRef(w)
    return ref && !ref.folder ? own.pages.find(p => p.name === ref.name) : undefined
  }

  const box = useRef(null)
  const [boxEl, setBoxEl] = useState(null)
  const holdBox = useCallback((n) => { box.current = n; setBoxEl(n) }, [])
  const cols = useGridColumns(boxEl)
  const fix = () => {
    setEditing(true)
    box.current?.scrollIntoView?.({ block: 'nearest' })
  }
  useEffect(() => {
    if (editRequest <= 0) return
    setEditing(true)
    box.current?.scrollIntoView?.({ block: 'nearest' })
  }, [editRequest])
  const widgets = useMemo(() => widgetList(wk.doc), [wk.doc])
  // The content height of each list and metric, as each reports it.
  const [reported, setReported] = useState({})
  const onUnits = useCallback((k, n) => setReported(r => (r[k] === n ? r : { ...r, [k]: n })), [])
  const unitsOf = (w) => widgetUnits(w, reported, conversations?.length || 0)
  const triggers = useHomeTriggers(widgets.some(w => w.type !== 'chats' && w.type !== 'page' && w.source))
  if (!wk.doc) return null
  const sizes = widgets.map(w => widgetSize(w, byKey.get(refKey(tileRef(w)))?.page))
  const scheduled = scheduledCount(widgets.filter(w => w.type !== 'chats' && w.type !== 'page'), triggers.triggers)

  return (
    <div className="wk-home" data-testid="home-widgets" ref={holdBox}>
      {wk.doc.exists && !wk.doc.valid && (
        <p className="wk-note wk-note--warn" role="status"><Icon name="warning" /> {t(wk.doc.fallback ? 'wellKnown.home.invalidFallback' : 'wellKnown.home.invalid')}</p>
      )}
      {widgets.length > 0 && (
        <PackedGrid sizes={sizes} units={widgets.map(unitsOf)} cols={cols} testId="home-widget-grid">
          {widgets.map((w, i) => (w.type === 'chats'
            ? <ChatsWidget key={w.id} w={w} conversations={conversations} onResume={onResume} />
            : w.type === 'page'
              ? <PageTile key={w.id} w={w} size={sizes[i]} listed={listedPage(w)} onFix={fix} />
              : <DataWidget key={w.id} w={w} triggers={triggers} onUnits={onUnits} />))}
        </PackedGrid>
      )}
      {editing ? (
        <LayoutEditor wk={wk} onDone={() => setEditing(false)} cols={cols} eligible={eligible} byKey={byKey} areaPages={areaPages} areaOn={own.pageAreaOn} unitsOf={unitsOf} />
      ) : (
        <div className="wk-layoutbar">
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => setEditing(true)} data-testid="home-edit-layout"><Icon name="pencil" /> {t('wellKnown.home.editLayout')}</button>
          <button type="button" className="wk-linkbtn" onClick={onAsk} data-testid="home-ask-layout"><Icon name="sparkles" /> {t('wellKnown.home.ask')}</button>
          {scheduled > 0 && <span className="wk-src" data-testid="home-schedule-note"><Icon name="clock" /> {t('wellKnown.home.footer.scheduleNote', { count: scheduled })}</span>}
        </div>
      )}
    </div>
  )
}
