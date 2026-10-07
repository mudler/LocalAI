import { useEffect, useId, useMemo, useRef, useState } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
// eslint-disable-next-line no-unused-vars
import ModelSelector from '../ModelSelector'
// eslint-disable-next-line no-unused-vars
import InstallNote from './InstallNote'
// eslint-disable-next-line no-unused-vars
import WorkThumb from './WorkThumb'
// eslint-disable-next-line no-unused-vars
import { StarGlyph } from './StarButton'
import { findParent } from '../../hooks/useWorkspace'
import { useResources } from '../../hooks/useResources'
import { useModelNeed } from '../../hooks/useModelSuggestion'
import { modelBudget } from '../../utils/modelBudget'
import { cssVars, fitFor, gbLabel, gbNumber } from '../../utils/modelLedger'
import { relativeTime } from '../../utils/format'
import { TYPE_ICON } from '../../utils/studioWork'
import './studio.css'
import './workspace.css'

// The shared frame of the seven Studio workspaces. A page keeps its own fields,
// endpoints and viewer; this file gives them one shape: a compose card (sources,
// prompt, a model chip, the essential options, an Advanced fold, an estimate and
// one action), a run area (a job card, a failure or the result with its
// toolbar), and a strip of recent results of the same type.

export function Workspace({ type, children }) {
  return (
    <div className="page-pad ws" data-testid="studio-workspace" data-type={type}>
      {children}
    </div>
  )
}

// A labelled control. `changed` outlines it when "Re-run with edits" has moved
// it away from the take it started from.
export function Field({ label, htmlFor, changed, hint, children, className = '' }) {
  return (
    <div className={`ws-field ${className}`.trim()} data-changed={changed || undefined}>
      {label && <label className="ws-label" htmlFor={htmlFor}>{label}</label>}
      {children}
      {hint && <p className="ws-hint">{hint}</p>}
    </div>
  )
}

// A native select drawn as a chip, for the few options that are always shown.
export function ChipSelect({ label, value, onChange, options, changed, mono = true, testId, disabled, id }) {
  const items = options.map(o => (typeof o === 'object' ? o : { value: o, label: o }))
  return (
    <span className="dk-select-wrap ws-chip" data-changed={changed || undefined}>
      <select
        id={id}
        className={`dk-select${mono ? ' dk-input--mono' : ''}`}
        aria-label={label}
        value={value}
        disabled={disabled}
        data-testid={testId}
        onChange={(e) => onChange(e.target.value)}
      >
        {items.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
      </select>
    </span>
  )
}

// An on/off option drawn as a chip. Pressed shows the kit's check mark.
export function ToggleChip({ label, on, onChange, changed, testId }) {
  return (
    <button type="button" className="dk-chip ws-toggle" aria-pressed={on} data-changed={changed || undefined} data-testid={testId} onClick={() => onChange(!on)}>
      {label}
    </button>
  )
}

// A short typed value drawn as a chip with its name in front: a duration, a
// frame rate.
export function ChipInput({ label, value, onChange, placeholder, type = 'text', step, min, changed, testId, width, id: given }) {
  const generated = useId()
  const id = given || generated
  return (
    <label className="ws-chip ws-chip--input" htmlFor={id} data-changed={changed || undefined}>
      <span>{label}</span>
      <input
        id={id}
        className="ws-chip__input"
        type={type}
        step={step}
        min={min}
        value={value}
        placeholder={placeholder}
        data-testid={testId}
        data-width={width}
        onChange={(e) => onChange(e.target.value)}
      />
    </label>
  )
}

// The model picker, drawn as a chip. It is the page's own ModelSelector
// (search, last model remembered); the fit sits in the estimate line.
export function ModelChip({ value, onChange, capability, options, loading, disabled, changed }) {
  return (
    <span className="ws-chip ws-chip--model" data-changed={changed || undefined}>
      <ModelSelector
        value={value}
        onChange={onChange}
        capability={capability}
        options={options}
        loading={loading}
        disabled={disabled}
        triggerClassName="ws-model"
      />
    </span>
  )
}

// Short example prompts, shown while the prompt is empty. The same starters the
// Studio front page offers for the type.
export function Starters({ type, onPick }) {
  const { t } = useTranslation('media')
  const list = t(`studio.starters.${type}`, { returnObjects: true })
  if (!Array.isArray(list) || list.length === 0) return null
  return (
    <div className="ws-starters" data-testid="ws-starters">
      <span className="studio-eyebrow">{t('studio.composer.starters')}</span>
      {list.map(s => <button key={s.label} type="button" className="dk-chip dk-chip--sm" onClick={() => onPick(s.text)}>{s.label}</button>)}
    </div>
  )
}

// A source that is optional: a dashed chip that opens a file picker. With a
// file chosen it shows the name and a way to remove it.
export function SourceChip({ label, accept, multiple = false, name, count = 0, preview, onFiles, onClear, testId }) {
  const id = useId()
  const filled = !!name || count > 0
  return (
    <span className={`ws-source${filled ? ' ws-source--filled' : ''}`} data-testid={testId}>
      <label className="ws-source__pick" htmlFor={id}>
        {preview ? <img src={preview} alt="" /> : <Icon name={filled ? 'check' : 'plus'} />}
        <span className="ws-source__text">
          <b>{label}</b>
          {filled && <small>{name || String(count)}</small>}
        </span>
      </label>
      <input
        id={id}
        className="ws-source__input"
        type="file"
        accept={accept}
        multiple={multiple}
        aria-label={label}
        onChange={(e) => { if (e.target.files?.length) onFiles(multiple ? [...e.target.files] : e.target.files[0]); e.target.value = '' }}
      />
      {filled && onClear && (
        <button type="button" className="ws-source__x" aria-label={`${label}: remove`} onClick={onClear}><Icon name="close" /></button>
      )}
    </span>
  )
}

// A source the run cannot start without: a recording. A drop area with a Choose
// button; with a file chosen it shows the name and size.
export function SourceDrop({ label, accept, file, onFile, hint, testId, inputId }) {
  const { t } = useTranslation('media')
  const generated = useId()
  const id = inputId || generated
  const [hover, setHover] = useState(false)
  const kb = file ? file.size / 1024 : 0
  const size = !file ? '' : kb >= 1024 ? `${(kb / 1024).toFixed(1)} MB` : `${Math.max(1, Math.round(kb))} KB`
  return (
    <div
      className={`ws-drop${file ? ' ws-drop--filled' : ''}${hover ? ' ws-drop--hover' : ''}`}
      data-testid={testId}
      onDragOver={(e) => { e.preventDefault(); setHover(true) }}
      onDragLeave={() => setHover(false)}
      onDrop={(e) => { e.preventDefault(); setHover(false); const f = e.dataTransfer.files?.[0]; if (f) onFile(f) }}
    >
      <Icon name={file ? 'check' : 'upload'} />
      <span className="ws-drop__text">
        <b>{file ? file.name : label}</b>
        <small>{file ? size : (hint || t('studio.workspace.dropHint'))}</small>
      </span>
      <label className="dk-btn dk-btn--secondary dk-btn--sm" htmlFor={id}>{file ? t('studio.workspace.replace') : t('studio.workspace.choose')}</label>
      <input
        id={id}
        className="ws-source__input"
        type="file"
        accept={accept}
        aria-label={label}
        onChange={(e) => { const f = e.target.files?.[0]; if (f) onFile(f); e.target.value = '' }}
      />
      {file && <button type="button" className="ws-source__x" aria-label={`${label}: remove`} onClick={() => onFile(null)}><Icon name="close" /></button>}
    </div>
  )
}

// A source chip that opens a panel with its own input (record, paste).
export function PanelChip({ label, open, onToggle, filled, controls }) {
  return (
    <button type="button" className={`ws-source ws-source--btn${filled ? ' ws-source--filled' : ''}`} aria-expanded={open} aria-controls={controls} onClick={onToggle}>
      <span className="ws-source__pick">
        <Icon name={filled ? 'check' : 'plus'} />
        <span className="ws-source__text"><b>{label}</b></span>
      </span>
    </button>
  )
}

// The Advanced fold: what is inside is named on the bar, so closing it does not
// hide that something is set.
export function Fold({ label, summary, open, onToggle, id, children }) {
  const body = useId()
  const bodyId = id || body
  return (
    <div className="ws-fold" data-open={open || undefined}>
      <button type="button" className="ws-fold__bar" aria-expanded={open} aria-controls={bodyId} onClick={onToggle}>
        <b>{label}</b>
        {summary && <span className="ws-fold__sum">{summary}</span>}
        <Icon name={open ? 'chevron-up' : 'chevron-down'} />
      </button>
      {open && <div id={bodyId} className="ws-fold__body">{children}</div>}
    </div>
  )
}

// The memory a model needs and whether it fits, when the gallery can say. No
// time is promised: nothing in the server can say how long a run will take.
// eslint-disable-next-line no-unused-vars
function Estimate({ model }) {
  const { t } = useTranslation('media')
  const { resources } = useResources(30000)
  const need = useModelNeed(model)
  const fit = need ? fitFor(need, modelBudget(resources)) : null
  if (!fit) return <span className="ws-estimate" data-testid="ws-estimate" />
  return (
    <span className="ws-estimate" data-testid="ws-estimate" data-fit={fit.state}>
      <Icon name="memory" />
      {fit.state === 'fits' && t('studio.composer.fit.fits', { need: gbLabel(fit.need), amount: gbNumber(fit.amount) })}
      {fit.state === 'spill' && t('studio.composer.fit.spill', { need: gbLabel(fit.need), amount: gbNumber(fit.amount) })}
      {fit.state === 'over' && t('studio.composer.fit.over', { need: gbLabel(fit.need), amount: gbNumber(fit.amount) })}
    </span>
  )
}

// The compose card.
//
//   icon, title, lede   what this workspace is
//   handoff             the hand-off note, when the page was opened from a result
//   sources             optional source chips (SourceChip, PanelChip)
//   panels              what a PanelChip opens
//   children            the prompt (and anything that belongs with it)
//   options             the model chip and the essential options
//   fold                the Advanced fold
//   model               the chosen model, for the fit line
//   noModel             { type, label, onChanged } when no model is installed
//   changes             what "Re-run with edits" has changed, from useWorkspace().changes
//   submit              { label, busyLabel, busy, disabled, why, icon }
export function ComposeCard({
  ws, icon, title, lede, onSubmit, handoff, sources, panels, children, options, fold, model, noModel, changes = [], submit,
}) {
  const { t } = useTranslation('media')
  return (
    <form className="ws-compose" onSubmit={onSubmit} ref={ws?.composeRef} data-testid="ws-compose" data-rerun={ws?.rerunning || undefined} noValidate>
      <header className="ws-compose__head">
        <h1 className="page-title ws-title">{icon && <Icon name={icon} />}{title}</h1>
        {lede && <span className="ws-lede">{lede}</span>}
      </header>
      {handoff}
      {sources && <div className="ws-sources" role="group" aria-label={t('studio.workspace.sourcesLabel')}>{sources}</div>}
      {panels}
      {children}
      {noModel && (
        <InstallNote key={noModel.type} type={noModel.type} typeLabel={noModel.label} onChanged={noModel.onChanged} className="ws-note" />
      )}
      {options && <div className="ws-options">{options}</div>}
      {fold}
      <footer className="ws-foot">
        <Estimate model={model} />
        {changes.length > 0 && (
          <span className="ws-changes" data-testid="ws-changes">
            <span>{t('studio.workspace.changedFrom')}</span>
            {changes.map(c => <code key={c.key}>{c.text}{c.long ? ` ${t('studio.workspace.edited')}` : ''}</code>)}
          </span>
        )}
        <span className="ws-go">
          {submit.why && <span className="ws-go__why" data-testid="ws-why">{submit.why}</span>}
          <button type="submit" className="dk-btn dk-btn--primary ws-go__btn" disabled={submit.disabled || submit.busy} aria-busy={submit.busy || undefined}>
            {!submit.busy && <Icon name={submit.icon || 'sparkles'} />}
            {submit.busy ? submit.busyLabel : (ws?.rerunning ? t('studio.workspace.runAgain') : submit.label)}
          </button>
        </span>
      </footer>
    </form>
  )
}

// --- The run area -------------------------------------------------------------

export function RunArea({ children }) {
  return <section className="media-result ws-run" data-testid="ws-run">{children}</section>
}

export function EmptyRun({ icon, text }) {
  return (
    <div className="ws-empty" data-testid="ws-empty">
      <Icon name={icon} />
      <p>{text}</p>
    </div>
  )
}

function useElapsed() {
  const [seconds, setSeconds] = useState(0)
  useEffect(() => {
    const id = setInterval(() => setSeconds(s => s + 1), 1000)
    return () => clearInterval(id)
  }, [])
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
}

// While a request is out. The server reports no phase and no percentage on
// these endpoints, so the bar is indeterminate and the only number shown is the
// time that has really passed.
//
//   label    "Making your picture"
//   detail   model, size and count as the request carries them
//   tiles    how many placeholder tiles to draw (pictures), 0 for none
export function JobCard({ label, detail, tiles = 0, ratio = 1 }) {
  const elapsed = useElapsed()
  const n = Math.min(Math.max(tiles, 0), 4)
  return (
    <div className="ws-job" role="status" aria-live="polite" data-testid="ws-job">
      <div className="ws-job__text">
        <p className="ws-job__title"><Icon name="spinner" spin /> <span>{label}</span> <span className="ws-job__time">{elapsed}</span></p>
        <span className="dk-progress ws-job__bar" role="progressbar" aria-label={label} data-indeterminate><span className="dk-progress-bar" /></span>
        {detail && <p className="ws-job__detail">{detail}</p>}
      </div>
      {n > 0 && (
        <div className={`ws-job__tiles ws-job__tiles--n${n}`} aria-hidden="true">
          {Array.from({ length: n }, (_, i) => <span key={i} className="dk-skeleton dk-skeleton--block ws-job__tile" style={cssVars({ '--ratio': ratio })} />)}
        </div>
      )}
    </div>
  )
}

// A run that did not finish: what the server said, that nothing was lost, and
// one action.
export function FailedCard({ message, onRetry, retryLabel }) {
  const { t } = useTranslation('media')
  return (
    <div className="ws-failed" role="alert" data-testid="ws-failed">
      <Icon name="alert-circle" />
      <div className="ws-failed__body">
        <h3>{t('studio.workspace.failed.title')}</h3>
        <p className="ws-failed__message">{message}</p>
        <p className="ws-failed__note">{t('studio.workspace.failed.note')}</p>
        <div className="ws-failed__row">
          {onRetry && <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" onClick={onRetry}><Icon name="repeat" /> {retryLabel || t('studio.workspace.failed.retry')}</button>}
          <a className="ws-link" href="/app/traces?tab=backend">{t('studio.workspace.failed.traces')}</a>
        </div>
      </div>
    </div>
  )
}

// --- The result ---------------------------------------------------------------

// eslint-disable-next-line no-unused-vars
function UseInMenu({ ws, item }) {
  const { t } = useTranslation('media')
  const [open, setOpen] = useState(false)
  const root = useRef(null)
  const steps = ws.steps(item)
  useEffect(() => {
    if (!open) return undefined
    const onDown = (e) => { if (root.current && !root.current.contains(e.target)) setOpen(false) }
    const onKey = (e) => { if (e.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => { document.removeEventListener('mousedown', onDown); document.removeEventListener('keydown', onKey) }
  }, [open])
  const none = steps.length === 0
  return (
    <span className="dk-anchor" ref={root}>
      <button
        type="button"
        className="dk-btn dk-btn--secondary dk-btn--sm"
        aria-haspopup="menu"
        aria-expanded={open}
        disabled={!item || none}
        data-testid="ws-use-in"
        title={none ? t('studio.lineage.noBranch', { type: t(`studio.tabs.${ws.type}`) }) : undefined}
        onClick={() => setOpen(v => !v)}
      >
        <Icon name="arrow-right" /> {t('studio.workspace.useIn')}
      </button>
      {open && (
        <div className="dk-menu dk-menu--end ws-menu" role="menu" aria-label={t('studio.workspace.useIn')}>
          {none && <p className="ws-menu__none">{t('studio.lineage.noBranch', { type: t(`studio.tabs.${ws.type}`) })}</p>}
          {steps.map(step => (
            <button
              key={step.edge}
              type="button"
              role="menuitem"
              className="dk-menu-item ws-menu__item"
              disabled={step.blocked}
              data-testid={`use-in-${step.edge}`}
              data-state={step.blocked ? 'unsupported' : step.missingModel ? 'missing' : 'ready'}
              onClick={() => { setOpen(false); ws.sendTo(item, step) }}
            >
              <Icon name={TYPE_ICON[step.to]} />
              <span className="ws-menu__text">
                <b>{step.title}</b>
                <small>
                  {step.blocked
                    ? t('studio.lineage.cannotStart', { page: t(`studio.tabs.${step.to}`), what: t(`studio.kinds.${step.source}`) })
                    : step.missingModel ? t('studio.lineage.needsModel', { type: t(`studio.tabs.${step.to}`) }) : t(`studio.steps.${step.edge}.why`)}
                </small>
              </span>
            </button>
          ))}
        </div>
      )}
    </span>
  )
}

// The result and what can be done with it.
//
//   item       the stored result, when there is one (Favourite, Use in and
//              Lineage need it); a result that was not stored has only Download
//   title      the heading; defaults to the prompt
//   meta       small facts under the viewer: model, size, seed
//   download   { href, name, testId }
//   onRerun    load this result's values into the form
//   actions    extra toolbar buttons that belong to the type (exports)
export function ResultCard({ ws, item, title, meta = [], download, onRerun, actions, children, footer, testId = 'ws-result' }) {
  const { t } = useTranslation('media')
  const parent = useMemo(() => findParent(item), [item])
  const take = item ? ws.canTake(item) : { ok: false }
  const heading = title || item?.title || t(`studio.work.untitled.${ws.type}`)
  const edgeLabel = item?.edge ? t(`studio.edges.${item.edge}`, { defaultValue: item.edge }) : ''
  return (
    <article className="ws-result" data-testid={testId} data-id={item?.id}>
      <header className="ws-result__head">
        <div className="ws-result__who">
          <h2 title={heading}>{heading}</h2>
          <p>{[t(`studio.tabs.${ws.type}`), item ? relativeTime(item.createdAt) : ''].filter(Boolean).join(' · ')}</p>
        </div>
        <div className="ws-result__tools" role="toolbar" aria-label={t('studio.workspace.tools')}>
          {item && (
            <button type="button" className="ws-tool" aria-pressed={item.favourite} aria-label={t('studio.work.favourite')} title={t('studio.work.favourite')} data-testid="ws-favourite" onClick={() => ws.toggleFavourite(item.id)}>
              <StarGlyph />
            </button>
          )}
          {download && (
            <a className="ws-tool" href={download.href} download={download.name} aria-label={t('studio.workspace.download')} title={t('studio.workspace.download')} data-testid={download.testId || 'ws-download'}>
              <Icon name="download" />
            </a>
          )}
          {actions}
          {item && <UseInMenu ws={ws} item={item} />}
          {onRerun && (
            <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={!item || !take.ok} title={take.ok ? undefined : t('studio.lineage.noInput')} data-testid="ws-rerun" onClick={() => onRerun(item)}>
              <Icon name="repeat" /> {t('studio.workspace.rerun')}
            </button>
          )}
          {item && (
            <button type="button" className="ws-tool" aria-label={t('studio.workspace.lineage')} title={t('studio.workspace.lineage')} data-testid="ws-lineage" onClick={() => ws.openLineage(item)}>
              <Icon name="git-branch" />
            </button>
          )}
        </div>
      </header>
      <div className="ws-result__view">{children}</div>
      <footer className="ws-result__meta">
        {meta.filter(Boolean).map((m, i) => <span key={i}>{m}</span>)}
        {item?.parentId && (
          parent
            ? <span data-testid="ws-from">{t('studio.workspace.from')} <Link to={`/app/studio?work=${encodeURIComponent(parent.id)}`}>{parent.title || t(`studio.work.untitled.${parent.type}`)}</Link> ({edgeLabel})</span>
            : <span data-testid="ws-from">{t('studio.workspace.fromGone', { edge: edgeLabel })}</span>
        )}
        {footer}
      </footer>
    </article>
  )
}

// A card in the result's shape for something that is not a result yet: the
// audio you chose, waiting to be transformed.
export function ViewCard({ title, sub, children, testId = 'ws-view' }) {
  return (
    <article className="ws-result" data-testid={testId}>
      <header className="ws-result__head">
        <div className="ws-result__who">
          <h2 title={title}>{title}</h2>
          {sub && <p>{sub}</p>}
        </div>
      </header>
      <div className="ws-result__view">{children}</div>
    </article>
  )
}

// --- The strip ----------------------------------------------------------------

const STRIP_MAX = 24

// Recent results of this type, from the shared history. Click one to show it
// above; click it again to go back to the latest.
export function ResultsStrip({ ws, selectedId, activeId, onSelect, onDelete, onClear }) {
  const { t } = useTranslation('media')
  const [filter, setFilter] = useState('all')
  const favouriteCount = ws.items.filter(i => i.favourite).length
  const shown = (filter === 'favourites' ? ws.items.filter(i => i.favourite) : ws.items).slice(0, STRIP_MAX)
  return (
    <section className="ws-strip media-history" data-testid="media-history" aria-label={t('studio.workspace.strip.title')}>
      <header className="ws-strip__head">
        <h2>{t('studio.workspace.strip.title')}</h2>
        <span className="ws-strip__count">{t('studio.work.results', { count: ws.items.length })}</span>
        <div className="ws-strip__tools">
          <div className="studio-filters ws-strip__filters" role="tablist" aria-label={t('studio.work.filters')}>
            <button type="button" role="tab" className="studio-filter" aria-selected={filter === 'all'} onClick={() => setFilter('all')}>{t('studio.work.all')}</button>
            <button type="button" role="tab" className="studio-filter" aria-selected={filter === 'favourites'} onClick={() => setFilter('favourites')}>
              {t('studio.work.favourites')} <span className="studio-filter__n">{favouriteCount}</span>
            </button>
          </div>
          {ws.items.length > 0 && (
            <button type="button" className="media-history-clear-btn ws-strip__clear" title={t('history.clearTitle')} aria-label={t('history.clearTitle')} onClick={onClear}>
              <Icon name="trash" />
            </button>
          )}
          <Link className="ws-link" to="/app/studio">{t('studio.workspace.strip.allWork')} <Icon name="arrow-right" /></Link>
        </div>
      </header>
      {shown.length === 0 ? (
        <p className="ws-strip__empty media-history-empty">{filter === 'favourites' ? t('studio.workspace.strip.noFavourites') : t('history.empty')}</p>
      ) : (
        <ul className="ws-strip__list">
          {shown.map(item => (
            <li key={item.id} className="ws-tile" data-testid="media-history-item" data-id={item.id} data-active={item.id === activeId || undefined}>
              <button type="button" className="ws-tile__open" aria-pressed={item.id === selectedId} aria-label={item.title || t(`studio.work.untitled.${ws.type}`)} onClick={() => onSelect(item.id)}>
                <WorkThumb item={item} compact />
                <span className="ws-tile__cap">
                  <span className="ws-tile__title">{item.title || t(`studio.work.untitled.${ws.type}`)}</span>
                  <small>{relativeTime(item.createdAt)}</small>
                </span>
              </button>
              {item.favourite && <span className="ws-tile__star" aria-hidden="true"><StarGlyph /></span>}
              <button type="button" className="ws-tile__del" aria-label={t('history.deleteEntry')} title={t('history.deleteEntry')} data-testid="media-history-delete" onClick={() => onDelete(item.id)}>
                <Icon name="close" />
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
