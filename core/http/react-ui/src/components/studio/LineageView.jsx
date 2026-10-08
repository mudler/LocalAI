import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
// eslint-disable-next-line no-unused-vars
import InstallNote from './InstallNote'
// eslint-disable-next-line no-unused-vars
import StarButton from './StarButton'
// eslint-disable-next-line no-unused-vars
import WorkThumb from './WorkThumb'
import { relativeTime } from '../../utils/format'
import { cssVars } from '../../utils/modelLedger'
import {
  NEXT_STEPS, TYPE_ICON, TYPE_INFO, canTake, handoffPath, layoutLineage, neighbour, projectOf, suggestNext,
} from '../../utils/studioWork'

// A prompt can be a paragraph; a title is one line of it.
function clip(text, max) {
  const s = String(text || '').trim()
  return s.length > max ? `${s.slice(0, max).trimEnd()}…` : s
}

function isTyping(el) {
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || el.isContentEditable
}

// A result, or a stack of results that came from each other, as a board: what it
// was made from on the left, what came out of it on the right, the path through
// the selection drawn heavier, and one dashed step suggested from the models that
// are installed.
//
// "Run as a new take" and "Branch from here" open the workspace with the source
// and the words filled in. They do not run anything here. A step the destination
// workspace cannot start from yet is listed but disabled, with the reason.
export default function LineageView({
  items, workId, modalities, defaultModel, onClose, onToggleFavourite, onHandoff, onModelsChanged,
}) {
  const { t } = useTranslation('media')
  const members = useMemo(() => projectOf(items, workId), [items, workId])
  const [selectedId, setSelectedId] = useState(workId)
  const [expanded, setExpanded] = useState(false)
  const [draft, setDraft] = useState(null)
  const boardRef = useRef(null)

  // The set of types that have a model, and the first model of each.
  const installed = useMemo(() => new Set(modalities.filter(m => m.installed.length > 0).map(m => m.key)), [modalities])
  const label = (key) => t(`studio.tabs.${key}`)

  const selected = members?.find(m => m.id === selectedId) || members?.[members.length - 1] || null
  const suggestion = selected ? suggestNext(selected.type, installed) : null
  const shownStep = draft ? { to: draft.to, edge: draft.edge } : suggestion
  const ghost = selected && shownStep
    ? { id: `ghost:${selected.id}`, from: `res:${selected.id}`, to: shownStep.to, edge: shownStep.edge, draft: !!draft }
    : null
  const layout = useMemo(
    () => (members ? layoutLineage(members, { selectedId: selected?.id, ghost }) : null),
    // ghost is derived from the three values below
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [members, selected?.id, ghost?.to, ghost?.edge, ghost?.draft],
  )

  const startDraft = (step) => {
    if (!step || !step.supported) return
    const text = step.edge === 'variation' ? (selected.title || '') : t(`studio.steps.${step.edge}.template`, { defaultValue: '' })
    setDraft({ to: step.to, edge: step.edge, text })
    setExpanded(true)
  }

  const take = () => {
    const ok = canTake(selected)
    if (!ok.ok) return
    const textLike = TYPE_INFO[selected.type].input === 'text'
    onHandoff(handoffPath(selected.type, {
      prompt: textLike ? selected.title : '',
      model: selected.model,
      size: selected.params?.size || '',
      from: selected.id,
      edge: 'take',
    }))
  }

  const openDraft = () => {
    if (!draft) return
    const textLike = TYPE_INFO[draft.to].input === 'text'
    const ids = modalities.find(m => m.key === draft.to)?.installed || []
    onHandoff(handoffPath(draft.to, {
      prompt: textLike ? draft.text : '',
      model: defaultModel(draft.to, ids),
      from: selected.id,
      edge: draft.edge,
    }))
  }

  // Keys: arrows walk the board, B branches, Esc closes the draft, then the
  // dock, then the view.
  const keys = useRef({})
  keys.current = { layout, selected, draft, expanded, suggestion, startDraft }
  // A layout effect, so the keys work from the first painted frame.
  useLayoutEffect(() => {
    const onKey = (e) => {
      const s = keys.current
      if (e.key === 'Escape') {
        if (s.draft) setDraft(null)
        else if (s.expanded) setExpanded(false)
        else onClose()
        return
      }
      if (isTyping(e.target) || e.ctrlKey || e.metaKey || e.altKey || !s.layout) return
      const dir = { ArrowLeft: 'left', ArrowRight: 'right', ArrowUp: 'up', ArrowDown: 'down' }[e.key]
      if (dir) {
        e.preventDefault()
        const next = neighbour(s.layout, s.selected?.id, dir)
        if (next) { setSelectedId(next); setDraft(null) }
      } else if (e.key === 'b' || e.key === 'B') {
        const step = s.suggestion || (NEXT_STEPS[s.selected?.type] || []).find(x => x.supported)
        if (step) { e.preventDefault(); s.startDraft(step) }
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  // Keep the selected node in view when it changes, sideways on a phone.
  useEffect(() => {
    const node = boardRef.current?.querySelector('[data-selected="true"]')
    node?.scrollIntoView?.({ block: 'nearest', inline: 'center' })
  }, [selected?.id])

  if (!members || !selected || !layout) {
    return (
      <div className="studio-lineage" data-testid="studio-lineage">
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={onClose}><Icon name="arrow-left" /> {t('studio.lineage.back')}</button>
        <div className="studio-empty"><b>{t('studio.lineage.goneTitle')}</b><span>{t('studio.lineage.goneBody')}</span></div>
      </div>
    )
  }

  const root = members[0]
  const steps = NEXT_STEPS[selected.type] || []
  const takeOk = canTake(selected)
  const branchStep = suggestion || steps.find(s => s.supported) || null
  const draftInstalled = draft ? installed.has(draft.to) : true
  const draftInfo = draft ? TYPE_INFO[draft.to] : null
  const draftTextLike = draftInfo?.input === 'text'
  const canOpenDraft = !!draft && draftInstalled && (!draftTextLike || draft.text.trim().length > 0)

  return (
    <div className="studio-lineage" data-testid="studio-lineage" data-count={members.length}>
      <header className="studio-lineage__head">
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={onClose} data-testid="studio-back">
          <Icon name="arrow-left" /> {t('studio.lineage.back')}
        </button>
        <div>
          <h1 className="studio-lineage__title" title={root.title || undefined}>{clip(root.title, 72) || label(root.type)}</h1>
          <p className="studio-lineage__sub">
            {members.length > 1 ? t('studio.lineage.sub', { count: members.length }) : t('studio.lineage.subOne')}
            {' · '}{relativeTime(members[members.length - 1].createdAt)}
          </p>
        </div>
      </header>

      <div className="studio-board" ref={boardRef} data-testid="studio-board" tabIndex={0} role="group" aria-label={t('studio.lineage.board')}>
        <div className="studio-board__canvas" style={cssVars({ '--w': `${layout.width}px`, '--h': `${layout.height}px` })}>
          <svg className="studio-edges" width={layout.width} height={layout.height} aria-hidden="true">
            {layout.edges.map(e => (
              <path key={e.id} d={e.d} data-chain={e.chain || undefined} data-ghost={e.ghost || undefined} />
            ))}
          </svg>
          {layout.edges.filter(e => e.label).map(e => (
            <span key={`l:${e.id}`} className="studio-edge-label" data-chain={e.chain || undefined} data-ghost={e.ghost || undefined}
              style={cssVars({ '--x': `${e.lx}px`, '--y': `${e.ly}px` })}>
              {t(`studio.edges.${e.label}`)}
            </span>
          ))}
          {layout.nodes.map(n => {
            const pos = cssVars({ '--x': `${n.x}px`, '--y': `${n.y}px`, '--w': `${n.w}px`, '--h': `${n.h}px` })
            if (n.kind === 'source') {
              return (
                <div key={n.id} className="studio-node studio-node--source" style={pos} data-testid="lineage-source">
                  {n.input === 'text'
                    ? <span className="studio-node__quote">{n.title}</span>
                    : <span className="studio-node__file"><Icon name={TYPE_INFO[n.type].source === 'image' ? 'image' : 'waveform'} /></span>}
                  <span className="studio-node__label">
                    <b><Icon name={n.input === 'text' ? 'pencil' : 'file'} />{n.input === 'text' ? t('studio.lineage.prompt') : t('studio.lineage.file')}</b>
                    <small>{relativeTime(n.createdAt)}</small>
                  </span>
                </div>
              )
            }
            if (n.kind === 'ghost') {
              const missing = !installed.has(n.ghost.to)
              return (
                <button
                  key={n.id}
                  type="button"
                  className="studio-node studio-node--ghost"
                  style={pos}
                  data-testid="lineage-ghost"
                  data-missing={missing || undefined}
                  aria-pressed={n.ghost.draft}
                  onClick={() => startDraft(steps.find(s => s.edge === n.ghost.edge))}
                >
                  <b><Icon name={TYPE_ICON[n.ghost.to]} />{t(`studio.steps.${n.ghost.edge}.title`)}</b>
                  <small>{missing ? t('studio.lineage.needsModel', { type: label(n.ghost.to) }) : (modalities.find(m => m.key === n.ghost.to)?.installed[0] || '')}</small>
                  <span className="dk-badge dk-badge--accent">{n.ghost.draft ? t('studio.lineage.draft') : t('studio.lineage.suggested')}</span>
                </button>
              )
            }
            const m = n.item
            const isSel = m.id === selected.id
            return (
              <button
                key={n.id}
                type="button"
                className="studio-node studio-node--result"
                style={pos}
                data-testid="lineage-node"
                data-type={m.type}
                data-id={m.id}
                data-selected={isSel}
                data-rel={layout.chain.has(n.id) ? '1' : '0'}
                aria-pressed={isSel}
                onClick={() => { setSelectedId(m.id); setDraft(null) }}
              >
                <WorkThumb item={m} compact />
                {isSel && <span className="studio-picked" aria-hidden="true"><Icon name="check" /></span>}
                <span className="studio-node__label">
                  <b><Icon name={TYPE_ICON[m.type]} /><span>{m.title || t(`studio.work.untitled.${m.type}`)}</span></b>
                  <small>{m.model || label(m.type)}</small>
                </span>
              </button>
            )
          })}
        </div>
      </div>

      <section className="studio-dock" data-open={expanded} data-testid="studio-dock" aria-label={t('studio.lineage.dock')}>
        <div className="studio-dock__row">
          <span className="studio-dock__thumb">
            {draft ? <Icon name={TYPE_ICON[draft.to]} /> : <WorkThumb item={selected} compact />}
          </span>
          <span className="studio-dock__who">
            {draft ? (
              <>
                <b>{t('studio.lineage.newBranch', { step: t(`studio.steps.${draft.edge}.title`) })}</b>
                <small>{t('studio.lineage.fromResult', { title: selected.title || label(selected.type) })}</small>
              </>
            ) : (
              <>
                <b>{selected.title || t(`studio.work.untitled.${selected.type}`)}</b>
                <small>{[selected.model, selected.elapsedMs ? t('studio.lineage.took', { seconds: Math.round(selected.elapsedMs / 1000) }) : '', relativeTime(selected.createdAt)].filter(Boolean).join(' · ')}</small>
              </>
            )}
          </span>
          <span className="studio-dock__actions">
            {draft ? (
              <>
                <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => setDraft(null)} data-testid="studio-draft-cancel">{t('studio.lineage.cancel')}</button>
                <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" disabled={!canOpenDraft} onClick={openDraft} data-testid="studio-draft-open">
                  <Icon name="sparkles" /> {t('studio.lineage.openIn', { type: label(draft.to) })}
                </button>
              </>
            ) : (
              <>
                <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" disabled={!takeOk.ok} onClick={take} data-testid="studio-take" title={takeOk.ok ? undefined : t('studio.lineage.noInput')}>
                  <Icon name="repeat" /> {t('studio.lineage.take')}
                </button>
                <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" disabled={!branchStep} onClick={() => startDraft(branchStep)} data-testid="studio-branch" title={branchStep ? undefined : t('studio.lineage.noBranch', { type: label(selected.type) })}>
                  <Icon name="git-branch" /> {t('studio.lineage.branch')}
                </button>
              </>
            )}
            <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" aria-expanded={expanded} aria-label={t('studio.lineage.details')} onClick={() => setExpanded(v => !v)} data-testid="studio-dock-toggle">
              <Icon name={expanded ? 'chevron-down' : 'chevron-up'} />
            </button>
          </span>
        </div>
        {(!takeOk.ok || !branchStep) && !draft && (
          <p className="studio-dock__hint" data-testid="studio-dock-hint">
            {!takeOk.ok && t('studio.lineage.noInput')}
            {!takeOk.ok && !branchStep && ' '}
            {!branchStep && t('studio.lineage.noBranch', { type: label(selected.type) })}
          </p>
        )}

        {expanded && (draft ? (
          <div className="studio-dock__body studio-draft" data-testid="studio-draft">
            <span className="studio-eyebrow">{t('studio.lineage.makeFrom')}</span>
            <div className="studio-draft__targets" role="group" aria-label={t('studio.lineage.targets')}>
              {steps.map(step => {
                const bad = !step.supported
                return (
                  <button
                    key={step.edge}
                    type="button"
                    className="dk-chip studio-type"
                    aria-pressed={draft.edge === step.edge}
                    data-missing={(!installed.has(step.to)) || undefined}
                    disabled={bad}
                    data-testid={`draft-target-${step.edge}`}
                    onClick={() => startDraft(step)}
                  >
                    <Icon name={TYPE_ICON[step.to]} />{t(`studio.steps.${step.edge}.title`)}
                  </button>
                )
              })}
            </div>
            {!draftInstalled && <InstallNote key={draft.to} type={draft.to} typeLabel={label(draft.to)} onChanged={onModelsChanged} />}
            {draftTextLike ? (
              <>
                <span className="studio-eyebrow">{t('studio.lineage.describe')}</span>
                <textarea className="studio-prompt studio-prompt--boxed" rows={3} value={draft.text} aria-label={t('studio.lineage.describe')} onChange={(e) => setDraft(d => ({ ...d, text: e.target.value }))} />
              </>
            ) : (
              <p className="studio-draft__plain">{t(`studio.steps.${draft.edge}.plain`)}</p>
            )}
            <p className="studio-draft__meta">{t('studio.lineage.keeps')}</p>
          </div>
        ) : (
          <div className="studio-dock__body studio-detail" data-testid="studio-detail">
            <div className="studio-detail__preview"><Preview item={selected} t={t} /></div>
            <div className="studio-detail__info">
              <span className="studio-eyebrow">{t('studio.lineage.promptLabel')}</span>
              <p className="studio-detail__prompt">{selected.title || t(`studio.work.untitled.${selected.type}`)}</p>
              <p className="studio-detail__meta">{[selected.model, selected.params?.size, selected.params?.speakers ? t('studio.lineage.speakers', { count: selected.params.speakers }) : ''].filter(Boolean).join(' · ')}</p>

              <span className="studio-eyebrow">{t('studio.lineage.continue')}</span>
              <ul className="studio-steps">
                {steps.length === 0 && <li className="studio-steps__none">{t('studio.lineage.noBranch', { type: label(selected.type) })}</li>}
                {steps.map(step => {
                  const bad = !step.supported
                  const missing = !bad && !installed.has(step.to)
                  return (
                    <li key={step.edge}>
                      <button type="button" className="studio-step" disabled={bad} onClick={() => startDraft(step)} data-testid={`step-${step.edge}`} data-state={bad ? 'unsupported' : missing ? 'missing' : 'ready'}>
                        <span className="studio-step__icon"><Icon name={TYPE_ICON[step.to]} /></span>
                        <span className="studio-step__text">
                          <b>{t(`studio.steps.${step.edge}.title`)}</b>
                          <small>
                            {bad
                              ? t('studio.lineage.cannotStart', { page: label(step.to), what: t(`studio.kinds.${step.source}`) })
                              : t(`studio.steps.${step.edge}.why`)}
                          </small>
                        </span>
                        {!bad && <span className={`dk-badge ${missing ? 'dk-badge--warn' : 'dk-badge--ok'}`}>{missing ? t('studio.lineage.needsModel', { type: label(step.to) }) : t('studio.lineage.ready')}</span>}
                      </button>
                    </li>
                  )
                })}
              </ul>
            </div>
            <div className="studio-detail__foot">
              <label className="studio-detail__fav"><StarButton on={selected.favourite} onToggle={() => onToggleFavourite(selected.id)} /><span>{t('studio.work.favourite')}</span></label>
              {selected.url && <a className="dk-btn dk-btn--ghost dk-btn--sm" href={selected.url} download><Icon name="download" /> {t('studio.lineage.download')}</a>}
            </div>
          </div>
        ))}
      </section>
    </div>
  )
}

// The large preview in the dock. Pictures and clips play from the server's own
// file; a mesh shows the picture it came from, because the viewer lives in its
// workspace.
// eslint-disable-next-line no-unused-vars
function Preview({ item, t }) {
  const kind = TYPE_INFO[item.type].kind
  if (kind === 'image' && item.url) return <img src={item.url} alt={item.title} />
  if (kind === 'video' && item.url) return <video src={item.url} controls preload="metadata" aria-label={item.title || t('studio.tabs.video')} />
  if (kind === 'audio' && item.url) {
    return (
      <div className="studio-detail__audio">
        <WorkThumb item={item} />
        <audio src={item.url} controls preload="none" aria-label={item.title || t('studio.tabs.sound')} />
      </div>
    )
  }
  return <WorkThumb item={item} />
}
