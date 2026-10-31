/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import Icon from '../Icon'
import Skeleton from '../Skeleton'
import { ProblemDetails } from './WellKnownCard'
import { useWellKnown } from '../../hooks/useWellKnown'
import { filesApi } from '../../utils/api'
import { widgetList, widgetData, cleanWidgets, move, sparkPath, formatMetric, wellKnownProblem, WIDGET_TYPES } from '../../utils/wellKnown'
import { cadenceOf, footerState, linkedTrigger, scheduledCount } from '../../utils/widgetTrigger'
import { useHomeTriggers } from '../../hooks/useHomeTriggers'
import WidgetFooter from './WidgetFooter'
import './wellKnown.css'

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
// resumes or retries the trigger.
function DataWidget({ w, triggers }) {
  const { t } = useTranslation('workspace')
  const wk = useWellKnown('widget-data', { file: w.source, enabled: !!w.source })
  const d = useMemo(() => (wk.error ? { state: 'error', errors: [wk.error.message], items: [], series: [], value: null } : widgetData(wk.doc)), [wk.doc, wk.error])
  const left = d.items?.filter(it => !it.done).length || 0
  const { trigger, proposal } = linkedTrigger(w, triggers.triggers, triggers.proposals)
  const state = d.state === 'loading' || !w.source ? '' : footerState({ data: d, trigger, proposal })
  const footId = `wk-foot-${w.id}`

  let body
  if (!w.source) body = <p className="wk-widget__empty">{t('wellKnown.home.noSource')}</p>
  else if (d.state === 'loading') body = <Skeleton variant="line" width="80%" />
  else if (d.state === 'empty' || (d.state === 'error' && !d.items.length && d.value === null)) {
    body = <p className="wk-widget__empty">{d.state === 'empty' ? t('wellKnown.home.footer.noData') : t('wellKnown.home.broken')}</p>
  } else if (w.type === 'metric') {
    const path = sparkPath(d.series, 240, 56)
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
    body = (
      <ul className="wk-wlist">
        {d.items.slice(0, 8).map((it, i) => (
          <li key={i} className={it.done ? 'is-done' : undefined}>
            <span className={`wk-box${it.done ? ' wk-box--done' : ''}`} aria-hidden="true">{it.done && <Icon name="check" />}</span>
            {it.link?.external ? (
              <a className="wk-t wk-link" href={it.link.href} target="_blank" rel="noopener noreferrer" title={t('wellKnown.home.opensSite', { host: it.link.host })} data-testid="widget-item-link">
                <span className="wk-link__text">{it.label}</span><Icon name="external-link" className="wk-link__ext" />
              </a>
            ) : it.link ? (
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
            {conversations.slice(0, 5).map(c => (
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

// LayoutEditor edits home.json: add, remove and reorder widgets, and pick the
// data file of each. The data files offered are the .json files of the home
// and of its data folder; any other path can be typed.
function LayoutEditor({ wk, onDone }) {
  const { t } = useTranslation('workspace')
  const [rows, setRows] = useState(() => widgetList(wk.doc))
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
  const save = async () => {
    setBusy(true)
    try {
      await wk.save({ widgets: cleanWidgets(rows) }, { version: baseVersion.current })
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
      {rows.length === 0 && <p className="wk-widget__empty">{t('wellKnown.home.noWidgets')}</p>}
      {rows.map((r, i) => (
        <div key={i} className="wk-wrow" data-testid="home-layout-row">
          <span className="dk-select-wrap">
            <select className="dk-select" aria-label={t('wellKnown.home.type')} value={r.type} onChange={e => set(i, 'type', e.target.value)}>
              {WIDGET_TYPES.map(ty => <option key={ty} value={ty}>{t(`wellKnown.home.types.${ty}`)}</option>)}
            </select>
          </span>
          <input className="dk-input" aria-label={t('wellKnown.home.title')} placeholder={t('wellKnown.home.title')} value={r.title} maxLength={60} onChange={e => set(i, 'title', e.target.value)} />
          <input
            className="dk-input dk-mono" list="wk-data-files" aria-label={t('wellKnown.home.source')} placeholder={r.type === 'chats' ? t('wellKnown.home.noSourceNeeded') : 'data/tasks.json'}
            value={r.type === 'chats' ? '' : r.source} disabled={r.type === 'chats'} onChange={e => set(i, 'source', e.target.value)}
          />
          <span className="wk-wrow__tools">
            <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm dk-btn--icon" aria-label={t('wellKnown.moveUp')} disabled={i === 0} onClick={() => setRows(move(rows, i, i - 1))}><Icon name="arrow-up" /></button>
            <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm dk-btn--icon" aria-label={t('wellKnown.moveDown')} disabled={i === rows.length - 1} onClick={() => setRows(move(rows, i, i + 1))}><Icon name="arrow-down" /></button>
            <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm dk-btn--icon" aria-label={t('wellKnown.remove')} onClick={() => setRows(rows.filter((_, j) => j !== i))}><Icon name="trash" /></button>
          </span>
        </div>
      ))}
      {problem && <div className="wk-note wk-note--error" role="alert"><Icon name="alert-circle" /><div><p>{problem.message}</p><ProblemDetails errors={problem.errors} /></div></div>}
      <div className="wk-actions">
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={add} disabled={rows.length >= 12} data-testid="home-layout-add"><Icon name="plus" /> {t('wellKnown.home.add')}</button>
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
  const box = useRef(null)
  useEffect(() => {
    if (editRequest <= 0) return
    setEditing(true)
    box.current?.scrollIntoView?.({ block: 'nearest' })
  }, [editRequest])
  const widgets = useMemo(() => widgetList(wk.doc), [wk.doc])
  const triggers = useHomeTriggers(widgets.some(w => w.type !== 'chats' && w.source))
  if (!wk.doc) return null
  const scheduled = scheduledCount(widgets.filter(w => w.type !== 'chats'), triggers.triggers)

  return (
    <div className="wk-home" data-testid="home-widgets" ref={box}>
      {wk.doc.exists && !wk.doc.valid && (
        <p className="wk-note wk-note--warn" role="status"><Icon name="warning" /> {t(wk.doc.fallback ? 'wellKnown.home.invalidFallback' : 'wellKnown.home.invalid')}</p>
      )}
      {widgets.length > 0 && (
        <div className="wk-widgets">
          {widgets.map(w => (w.type === 'chats'
            ? <ChatsWidget key={w.id} w={w} conversations={conversations} onResume={onResume} />
            : <DataWidget key={w.id} w={w} triggers={triggers} />))}
        </div>
      )}
      {editing ? (
        <LayoutEditor wk={wk} onDone={() => setEditing(false)} />
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
