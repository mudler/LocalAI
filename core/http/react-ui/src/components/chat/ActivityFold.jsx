import { useContext, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { renderMarkdown, highlightAll } from '../../utils/markdown'
import { toolLine, toolOutput } from '../../utils/chatThread'
import { groupSteps, groupFacts, liveSteps, thoughtGist, turnFacts } from '../../utils/chatLive'
import { addonChange, toolRefusal } from '../../utils/addonChange'
import { CHANGE_CARD, triggerCards } from '../../utils/chatProposal'
import { changeProposalIds } from '../../utils/changeProposal'
import ChangeProposalCard from './ChangeProposalCard'
import { toolLabel, formatDuration, runTimes } from '../../utils/toolLabels'
import Icon from '../Icon'
import { openChatFile } from './chatActions'
// eslint-disable-next-line no-unused-vars
import ChangeNote from './ChangeNote'
// eslint-disable-next-line no-unused-vars
import ChatAppFrame from '../ChatAppFrame'
// eslint-disable-next-line no-unused-vars
import ProposalCard from './ProposalCard'
import TriggerChangeCard from './TriggerChangeCard'
// eslint-disable-next-line no-unused-vars
import CommandsConsentCard from './CommandsConsentCard'
import NetworkConsentCard from './NetworkConsentCard'
import { EgressCardsContext } from './egressCardsContext'
import { providerTag } from '../../utils/chatCommands'
import { useAddonTitle } from '../../addons/useAddonRegistry'

// What the thread says about reasoning and tool use: one quiet line that opens
// inline into the steps. Everything it shows is in the transcript the server
// keeps: reasoning lines and tool lines ("name: summary", then a capped output).
//
// A step reads as a person would say it ("Read quote-acme.pdf", "Listed
// /Contracts · 3 files") with its time on the right. The tool id, its input and
// its output are one click away, for debugging.

// Output longer than this many lines shows its start and a "Show all" button.
const OUTPUT_LINES = 12

// A thought line holds two lines of text, so the cut of its words is made at
// the width of the screen, not by the browser in the middle of a word.
const NARROW = '(max-width: 640px)'
const GIST_CHARS = { wide: 120, narrow: 64 }
function useGistChars() {
  const media = () => (typeof window !== 'undefined' && window.matchMedia ? window.matchMedia(NARROW) : null)
  const [narrow, setNarrow] = useState(() => Boolean(media()?.matches))
  useEffect(() => {
    const query = media()
    if (!query) return undefined
    const on = (e) => setNarrow(e.matches)
    query.addEventListener('change', on)
    return () => query.removeEventListener('change', on)
  }, [])
  return narrow ? GIST_CHARS.narrow : GIST_CHARS.wide
}

// The words of a run: "Worked for 1.2 s · 4 steps", "Read quote-acme.pdf",
// "Thought for 2 s".
function summarise(items, t, lng) {
  const calls = items.filter(i => i.role === 'tool')
  const { total } = runTimes(items)
  const duration = total !== null ? formatDuration(total, t, lng) : ''
  if (calls.length === 0) {
    if (items.some(i => i.role === 'reasoning')) {
      return duration ? t('steps.thoughtFor', { duration }) : t('activity.thought')
    }
    return t('activity.result')
  }
  if (calls.length === 1 && !duration) {
    const { name, summary } = toolLine(calls[0].text)
    return labelText(toolLabel(name, summary, ''), t)
  }
  // The steps are the thoughts and the calls: what the list under the line shows.
  const count = t('steps.count', { count: calls.length + items.filter(i => i.role === 'reasoning').length })
  return duration ? `${t('steps.worked', { duration })} · ${count}` : count
}

// A label as plain text, for the fold line.
function labelText(label, t) {
  const verb = t(`steps.verb.${label.verb}`)
  return [verb, label.verb === 'used' ? label.tool : '', label.arg].filter(Boolean).join(' ')
}

// The line of a fold: what the run was, and a chevron. The live turn draws it
// too (StreamingFlow), so the line is the same one when the turn is stored.
// eslint-disable-next-line no-unused-vars
function FoldHead({ summary, open, live, working, onToggle, icon, controls, failed = 0 }) {
  const { t } = useTranslation('chat')
  return (
    <button
      type="button"
      className="cx-fold__head"
      aria-expanded={open}
      aria-controls={open ? controls : undefined}
      onClick={onToggle}
    >
      <Icon name={icon} />
      <span className="cx-fold__label">{summary}</span>
      {failed > 0 && <span className="cx-badge" data-testid="chat-fold-failed">{t('steps.group.failed', { count: failed })}</span>}
      {working && <span className="cx-dots cx-dots--inline" aria-hidden="true"><span /><span /><span /></span>}
      <Icon name="chevron-right" className="cx-fold__chev" />
    </button>
  )
}

// eslint-disable-next-line no-unused-vars
function FoldFrame({ summary, open, live, working, onToggle, icon, children, id, failed = 0 }) {
  const bodyId = `${id}-body`
  return (
    <div className="cx-fold" data-open={open || undefined} data-live={live || undefined} data-testid="chat-activity">
      <FoldHead summary={summary} open={open} working={working} onToggle={onToggle} icon={icon} controls={bodyId} failed={failed} />
      {open && <div className="cx-fold__body" id={bodyId}>{children}</div>}
    </div>
  )
}

// The verb, the argument and what came back, as a person reads them.
// eslint-disable-next-line no-unused-vars
function ToolText({ label, working = false }) {
  const { t } = useTranslation('chat')
  return (
    <>
      <b className="cx-step__verb">{t(`steps.verb.${label.verb}`)}</b>
      {label.verb === 'used' && <code className="cx-step__tool">{label.tool || t('activity.tool')}</code>}
      {label.arg && (label.file ? (
        // The file a step saved opens in the Files pane. The step line is not
        // a button when it holds this one (see Step), so they do not nest.
        <button
          type="button" className={`cx-step__file${working ? ' cx-step__file--working' : ''}`}
          title={working ? t('steps.workingTitle', { path: label.argTitle }) : t('steps.openFile', { path: label.argTitle })}
          data-testid="chat-step-file" data-kind={working ? 'working' : undefined}
          onClick={(e) => { e.stopPropagation(); openChatFile(label.argTitle) }}
        >
          <Icon name={working ? 'file-code' : 'file-text'} />{label.arg}
        </button>
      ) : (
        <span className="cx-step__arg" title={label.argTitle}>{label.arg}</span>
      ))}
      {label.result && <span className="cx-step__meta">· {t(label.result.key, label.result.values)}</span>}
    </>
  )
}

// A long output shows its first lines; the rest is one click away.
// eslint-disable-next-line no-unused-vars
function Output({ text }) {
  const { t } = useTranslation('chat')
  const [all, setAll] = useState(false)
  const lines = String(text).replace(/\n+$/, '').split('\n')
  const long = lines.length > OUTPUT_LINES
  return (
    <div className="cx-step__out">
      <pre className="cx-out" data-testid="chat-step-output">{long && !all ? lines.slice(0, OUTPUT_LINES).join('\n') : lines.join('\n')}</pre>
      {long && (
        <button type="button" className="cx-step__more" onClick={() => setAll(v => !v)} aria-expanded={all}>
          {all ? t('steps.showLess') : t('steps.showAll', { count: lines.length })}
        </button>
      )}
    </div>
  )
}

// The tool as the server knows it: its id, what it was called with and its
// output.
// eslint-disable-next-line no-unused-vars
function ToolDetail({ name, summary, output }) {
  const { t } = useTranslation('chat')
  return (
    <>
      <dl className="cx-step__facts">
        <div><dt>{t('steps.tool')}</dt><dd><code>{name || t('activity.tool')}</code></dd></div>
        {summary && <div><dt>{t('steps.input')}</dt><dd><code>{summary}</code></dd></div>}
      </dl>
      {output && <Output text={output} />}
    </>
  )
}

// One step. The line toggles its detail when it has one. state is running,
// done, failed or thought. A line that holds its own button (the file chip,
// split) is not a button: a click on it still toggles, and the chevron is
// the button for the keyboard. defaultOpen follows the state of the step (a
// failed call opens, a finished thought folds) until the reader clicks the
// line: then what the reader chose stays.
// eslint-disable-next-line no-unused-vars
function Step({ state, duration, children, detail, defaultOpen = false, testId, split = false, after = null, className = '' }) {
  const { t } = useTranslation('chat')
  const [manual, setManual] = useState(null)
  const open = manual ?? defaultOpen
  const toggle = () => setManual(!open)
  const icon = { running: 'spinner', done: 'check', failed: 'alert-circle', thought: 'lightbulb' }[state]
  const status = { running: t('steps.running'), failed: t('steps.failed') }[state]
  const head = (
    <>
      <span className="cx-step__icon" key={state}>
        <Icon name={icon} spin={state === 'running'} />
        {status && <span className="sr-only">{status}</span>}
      </span>
      <span className="cx-step__line">{children}</span>
      {duration ? <span className="cx-step__dur">{duration}</span> : <span />}
      {detail && split ? (
        <button type="button" className="cx-step__toggle" aria-expanded={open} aria-label={t('steps.details')} onClick={(e) => { e.stopPropagation(); toggle() }}>
          <Icon name="chevron-right" className="cx-step__chev" />
        </button>
      ) : detail ? <Icon name="chevron-right" className="cx-step__chev" /> : <span />}
    </>
  )
  return (
    <li className={`cx-step ${className}`} data-state={state} data-open={(detail && open) || undefined} data-testid={testId}>
      {detail && split ? (
        // A click anywhere on the line toggles; the chevron is the keyboard control.
        <div className="cx-step__head cx-step__head--split" onClick={toggle}>{head}</div>
      ) : detail ? (
        <button type="button" className="cx-step__head" aria-expanded={open} onClick={toggle}>{head}</button>
      ) : (
        <div className="cx-step__head">{head}</div>
      )}
      {after}
      {detail && open && <div className="cx-step__detail">{detail}</div>}
    </li>
  )
}

// Reasoning as Markdown, with its code highlighted.
// eslint-disable-next-line no-unused-vars
function Reasoning({ text, live }) {
  const ref = useRef(null)
  useEffect(() => {
    if (!ref.current) return
    if (live) ref.current.scrollTop = ref.current.scrollHeight
    else highlightAll(ref.current)
  }, [text, live])
  return (
    <div
      ref={ref}
      className={`cx-think${live ? ' cx-think--live' : ''}`}
      dangerouslySetInnerHTML={{ __html: renderMarkdown(text || '') }}
    />
  )
}

// A reasoning line of the transcript or of the live turn. Folded, it is one
// line of its words (see thoughtGist); a thought that is still arriving shows
// its latest line and is the one that shimmers. The arriving thought stays one
// line high (cx-step--live): a gist that wrapped to two lines and back as it
// streamed would move everything under it, and the pinned thread with it.
// eslint-disable-next-line no-unused-vars
function ThoughtStep({ text, duration, live }) {
  const { t } = useTranslation('chat')
  const gist = thoughtGist(text, { tail: live, max: useGistChars() })
  const more = live || gist.more
  return (
    <Step
      state="thought"
      className={`cx-step--thought${live ? ' cx-step--live' : ''}`}
      duration={duration}
      detail={more ? <Reasoning text={text} live={live} /> : null}
    >
      <b className={`cx-step__verb${live ? ' cx-shimmer' : ''}`}>{live ? t('steps.thinking') : t('activity.thought')}</b>
      <span className="cx-step__gist">{gist.text}</span>
    </Step>
  )
}

// What the model said before a tool call. It is quiet commentary in the flow,
// not the answer: smaller and muted. The turn's answer is the text after the
// last call, and it is the same element until a call follows it (see
// StreamingFlow), so the change of style is one short transition.
// eslint-disable-next-line no-unused-vars
function NoteLine({ text }) {
  return (
    <li className="cx-note" data-testid="chat-note">
      <div className="cx-prose" dangerouslySetInnerHTML={{ __html: renderMarkdown(text || '') }} />
    </li>
  )
}

// A run of calls with nothing between them, as one line: "Used 5 tools" and its
// time. While it runs the line is the call that runs now. A failed call shows
// as a badge on the line, and the group opens on it. Each call is a step of the
// group, with the input and the output one click away.
// eslint-disable-next-line no-unused-vars
function ToolGroup({ facts, label, live, children, duration }) {
  const { t } = useTranslation('chat')
  const [manual, setManual] = useState(null)
  const running = facts.active >= 0
  const open = manual ?? (facts.failed > 0 && !running)
  const state = running ? 'running' : facts.failed > 0 ? 'failed' : 'done'
  const icon = { running: 'spinner', done: 'check', failed: 'alert-circle' }[state]
  const text = running
    ? `${label} · ${t('steps.group.calls', { count: facts.count })}`
    : t('steps.group.used', { count: facts.count })
  return (
    <li className="cx-step cx-group" data-state={state} data-open={open || undefined} data-testid="chat-step-group">
      <button type="button" className="cx-step__head" aria-expanded={open} onClick={() => setManual(!open)}>
        <span className="cx-step__icon" key={state}>
          <Icon name={icon} spin={running} />
          {running && <span className="sr-only">{t('steps.running')}</span>}
        </span>
        <span className="cx-step__line">
          <b className={`cx-step__verb${running ? ' cx-shimmer' : ''}`}>{text}</b>
          {facts.failed > 0 && <span className="cx-badge" data-testid="chat-step-group-failed">{t('steps.group.failed', { count: facts.failed })}</span>}
        </span>
        {duration ? <span className="cx-step__dur">{duration}</span> : <span />}
        <Icon name="chevron-right" className="cx-step__chev" />
      </button>
      {open && <ol className="cx-group__steps">{children}</ol>}
    </li>
  )
}

const baseName = (p) => String(p || '').split('/').pop()

// One file a command saved: a chip that opens it in the preview pane, or, for
// a deleted file, its name struck out.
// eslint-disable-next-line no-unused-vars
function SavedFile({ file: f, t }) {
  if (f.op === 'deleted') {
    return (
      <span className="cx-step__gone" title={f.path} data-op="deleted">
        <del>{baseName(f.path)}</del> <span className="cx-step__meta">{t('steps.fileOp.deleted')}</span>
      </span>
    )
  }
  const working = f.kind === 'working'
  return (
    <button
      type="button" className={`cx-step__file${working ? ' cx-step__file--working' : ''}`} title={t('steps.openFile', { path: f.path })}
      data-testid="chat-step-saved-file" data-op={f.op} data-kind={f.kind || undefined}
      onClick={(e) => { e.stopPropagation(); openChatFile(f.path) }}
    >
      <Icon name={working ? 'file-code' : 'file-text'} />{baseName(f.path)}
      {f.op === 'changed' && <span className="cx-step__op">{t('steps.fileOp.changed')}</span>}
    </button>
  )
}

// The files a command saved in the user's files (card.files of its step): one
// chip a file, which opens it in the preview pane like the file of a
// write_file step. The working files (kind working, set by the server) fold
// into one quiet line, "Also saved 2 working files", that opens to their chips.
// eslint-disable-next-line no-unused-vars
function StepFiles({ files, more }) {
  const { t } = useTranslation('chat')
  const [open, setOpen] = useState(false)
  if (!files?.length) return null
  const main = files.filter(f => f.kind !== 'working')
  const working = files.filter(f => f.kind === 'working')
  return (
    <div className="cx-step__files" data-testid="chat-step-files">
      {main.length > 0 && <span className="cx-step__files-label">{t('steps.savedFiles')}</span>}
      {main.map(f => <SavedFile key={f.path} file={f} t={t} />)}
      {working.length > 0 && (
        <button
          type="button" className="cx-step__working" aria-expanded={open} data-testid="chat-step-working"
          onClick={(e) => { e.stopPropagation(); setOpen(v => !v) }}
        >
          {t(main.length > 0 ? 'steps.alsoSavedWorking' : 'steps.savedWorking', { count: working.length })}
          <Icon name="chevron-right" className="cx-step__working-chev" />
        </button>
      )}
      {open && working.map(f => <SavedFile key={f.path} file={f} t={t} />)}
      {more > 0 && <span className="cx-step__meta">{t('steps.moreFiles', { count: more })}</span>}
    </div>
  )
}

// One stored tool line as a step. The note of an add-on change (with Undo) is
// not in the fold: it shows under it, see ActivityGroup.
// eslint-disable-next-line no-unused-vars
function ToolStep({ text, duration, card }) {
  const { t: tc } = useTranslation('chats')
  const { name, summary } = toolLine(text)
  // A replaceable tool (bash) says who served it: an add-on, by the title it
  // declares, or this machine.
  const tag = providerTag(card)
  const providerTitle = useAddonTitle(card?.kind === 'tool_provider' ? String(card.id || '') : '')
  const out = toolOutput(text)
  const refused = Boolean(toolRefusal(text))
  const shownOut = out && !addonChange(text) && !refused ? out : ''
  const label = toolLabel(name, summary, refused ? '' : out)
  return (
    <Step
      state={refused ? 'failed' : 'done'}
      defaultOpen={refused}
      duration={duration}
      split={label.file && Boolean(label.arg)}
      testId="chat-step"
      detail={<ToolDetail name={name} summary={summary} output={shownOut} />}
      after={<StepFiles files={card?.files} more={card?.files_more || 0} />}
    >
      <ToolText label={label} working={card?.file_kind === 'working'} />
      {tag && <span className="cx-step__provider" data-testid="chat-step-provider" data-provider={card.kind}>{tc(`tools.provider.${tag.key}`, providerTitle ? { ...tag.values, name: providerTitle } : tag.values)}</span>}
    </Step>
  )
}

// A run of reasoning, notes and tool lines between two messages, in the order
// they happened, under one fold line ("Worked for 5.6 s · 7 steps"). A thought
// is one line, a note is quiet commentary, a call is a line of its own, and
// calls in a row group from GROUP_MIN calls (the same rule as the live turn).
// A stored turn starts folded. Add-on changes, refusals and trigger proposals
// are listed under the fold, so their buttons and reasons stay in sight.
export function ActivityGroup({ items, id, proposalTick = 0, withProposals = true, failedHint = 0 }) {
  const [expanded, setExpanded] = useState(false)
  const { t, i18n } = useTranslation('chat')
  const { t: tc } = useTranslation('chats')

  if (!items || items.length === 0) return null
  const changes = items.filter(item => item.role === 'tool' && (addonChange(item.text) || toolRefusal(item.text)))
  const icon = items.some(i => i.role === 'reasoning') ? 'lightbulb' : 'wrench'
  const { steps } = runTimes(items)
  const dur = (i) => (steps[i] !== null ? formatDuration(steps[i], t, i18n.language) : '')
  const entries = items.map((item, idx) => ({ kind: item.role === 'tool' ? 'tool' : item.role === 'note' ? 'note' : 'thought', key: `i${idx}`, idx, item }))
  const flow = groupSteps(entries.filter(e => e.kind !== 'note' || e.item.text?.trim()))
  const toolStep = (e) => <ToolStep key={e.key} text={e.item.text} duration={dur(e.idx)} card={e.item.card} />

  const list = (
        <ol className="cx-steps">
          {flow.map((e) => {
            if (e.kind === 'thought') return <ThoughtStep key={e.key} text={e.item.text} duration={dur(e.idx)} />
            if (e.kind === 'note') return <NoteLine key={e.key} text={e.item.text} />
            if (e.kind === 'group') {
              const gone = e.tools.filter(x => toolRefusal(x.item.text)).length
              const known = e.tools.map(x => steps[x.idx])
              const total = known.every(x => x !== null) ? known.reduce((a, x) => a + x, 0) : null
              return (
                <ToolGroup
                  key={e.key}
                  facts={{ count: e.tools.length, failed: gone, active: -1, total }}
                  duration={total !== null ? formatDuration(total, t, i18n.language) : ''}
                >
                  {e.tools.map(toolStep)}
                </ToolGroup>
              )
            }
            return toolStep(e)
          })}
        </ol>
  )

  return (
    <>
      <FoldFrame
        id={id}
        icon={icon}
        summary={summarise(items, t, i18n.language)}
        open={expanded}
        failed={Math.max(failedHint || 0, items.filter(i => i.role === 'tool' && toolRefusal(i.text)).length)}
        onToggle={() => setExpanded(v => !v)}
      >
        {list}
      </FoldFrame>
      {changes.map((item) => <ChangeNote key={`c-${item.seq}`} text={item.text} t={tc} />)}
      <NetworkCards items={items} />
      {withProposals && <ActivityProposals items={items} proposalTick={proposalTick} />}
    </>
  )
}

// The network cards that the bash calls of a run of tool lines asked, under the
// fold, so a reload shows them where they were asked.
function NetworkCards({ items }) {
  const { byTool, chatId, egress } = useContext(EgressCardsContext)
  const cards = items.filter(item => item.role === 'tool').flatMap(item => byTool.get(item.seq) || [])
  return cards.map(c => <NetworkConsentCard key={c.id} card={c} chatId={chatId} egress={egress} />)
}

// The trigger proposals and trigger changes of a run of tool lines, as cards. A
// message draws them below its text, because the card is where the message
// ends; a run with no message after it (ActivityRow) draws them with the fold.
export function ActivityProposals({ items, proposalTick = 0 }) {
  const { t: tc } = useTranslation('chats')
  return [
    ...triggerCards(items).map(({ kind, id }) => (kind === CHANGE_CARD
      ? <TriggerChangeCard key={`tc-${id}`} id={id} t={tc} tick={proposalTick} />
      : <ProposalCard key={`p-${id}`} id={id} t={tc} tick={proposalTick} />)),
    // A change a model proposed to a memory note, a profile or another kind.
    ...changeProposalIds(items).map((id) => <ChangeProposalCard key={`cp-${id}`} id={id} t={tc} tick={proposalTick} />),
  ]
}

// A tool call that has an MCP App: the tool line, then the app in its frame.
// The live call and the stored line use this one component at the same key, so
// the frame stays mounted when the turn ends. While the live card is read it
// shows a loading line; when it cannot be read, the output as text.
export function AppRow({ chatId, row, onSend }) {
  const { t: tc } = useTranslation('chats')
  const tool = row.live ? row.tool : null
  const { name, summary } = row.live ? { name: tool.name, summary: tool.summary } : toolLine(row.m.text)
  const label = toolLabel(name, summary, '')
  const app = row.live ? (tool.appState === 'ready' ? tool.app : undefined) : row.m.app
  const loading = row.live && tool.appState === 'loading'
  const fallback = row.live && tool.appState === 'failed' ? (tool.output || '') : undefined
  return (
    <div className="cx-row" data-testid="chat-app-row">
      <div className="cx-who cx-who--tool" title={name}><Icon name="puzzle" /><span className="cx-step__line"><ToolText label={label} /></span></div>
      {!row.live && <ChangeNote text={row.m.text} t={tc} />}
      {app ? <ChatAppFrame chatId={chatId} app={app} onSend={onSend} t={tc} /> : null}
      {!app && loading ? <div className="ch-app" data-testid="chat-app"><div className="ch-app__state">{tc('app.loading')}</div></div> : null}
      {!app && !loading && fallback !== undefined ? (
        <div className="ch-app" data-testid="chat-app">
          <div className="ch-app__state" data-testid="chat-app-fallback">
            <div>{tc('app.unavailable', { reason: tc('app.notRead') })}</div>
            {fallback && <pre className="ch-app__text">{fallback}</pre>}
          </div>
        </div>
      ) : null}
    </div>
  )
}

// The turn while it runs, in the order it happened: a thought line, the note
// the model said before a call, the call; calls in a row group. The text after
// the last call is the answer, drawn by renderText (the page's prose) in the
// same list, so the text that is the answer so far is the same element when a
// call follows it and it becomes a note. Only the last step is active: the one
// thing that shimmers or spins. When nothing is, three quiet dots show that the
// model works on the next step.
export function StreamingFlow({ live, renderText }) {
  const { t, i18n } = useTranslation('chat')
  const lng = i18n.language
  const { steps, answer, answerKey } = liveSteps(live)
  const flow = groupSteps(steps)
  const [manual, setManual] = useState(null)
  const ended = Boolean(live.ended)
  const folded = ended && !manual
  const fmt = (ms) => (Number.isFinite(ms) && ms >= 500 ? formatDuration(ms, t, lng) : '')
  const toolRow = (e) => {
    const tc = e.tool
    const state = tc.state === 'running' ? 'running' : (tc.state === 'failed' ? 'failed' : 'done')
    const label = toolLabel(tc.name, tc.summary, state === 'failed' ? '' : tc.output)
    return (
      <Step
        key={e.key}
        state={state}
        duration={state === 'running' ? '' : fmt(e.ms)}
        testId="chat-step"
        split={label.file && Boolean(label.arg)}
        defaultOpen={state === 'failed'}
        detail={<ToolDetail name={tc.name} summary={tc.summary} output={tc.output} />}
      >
        <ToolText label={label} />
      </Step>
    )
  }
  const consents = steps.filter(e => e.kind === 'tool' && e.tool.state === 'running' && e.tool.consent?.id).map(e => e.tool.consent)
  const last = flow[flow.length - 1]
  const busy = !answer && !ended && !(last && ((last.kind === 'thought' && last.open) || (last.kind === 'tool' && last.tool.state === 'running') || (last.kind === 'group' && last.tools.some(x => x.tool.state === 'running'))))
  if (flow.length === 0 && !answer) return null
  // The line of the finished turn, as the stored turn draws it (summarise). It
  // is in the list from the start, with no height, and the steps fold to no
  // height when the turn ends: one move, in place.
  const facts = turnFacts(live)
  const worked = facts.ms !== null ? fmt(Math.max(facts.ms, 500)) : ''
  const count = t('steps.count', { count: facts.steps })
  const summary = facts.calls === 0
    ? (worked ? t('steps.thoughtFor', { duration: worked }) : t('activity.thought'))
    : (worked ? `${t('steps.worked', { duration: worked })} · ${count}` : count)
  return (
    <>
      <ol className="cx-steps cx-flow" id="cx-live-flow" data-testid="chat-flow" data-live data-ended={ended || undefined} data-folded={folded || undefined}>
        {flow.length > 0 && (
          <li className="cx-flow__fold">
            <div className="cx-fold" data-open={!folded || undefined}>
              <FoldHead
                summary={summary}
                failed={steps.filter(e => e.kind === 'tool' && e.tool.state === 'failed').length}
                icon={facts.thoughts > 0 ? 'lightbulb' : 'wrench'}
                open={!folded}
                controls="cx-live-flow"
                onToggle={() => setManual(folded)}
              />
            </div>
          </li>
        )}
        {flow.map((e) => {
          if (e.kind === 'thought') return <ThoughtStep key={e.key} text={e.text} duration={e.open ? '' : fmt(e.ms)} live={e.open} />
          if (e.kind === 'note') return renderText({ key: e.key, text: e.text, note: true })
          if (e.kind === 'group') {
            const facts = groupFacts(e.tools)
            const activeTool = facts.active >= 0 ? e.tools[facts.active].tool : null
            const label = activeTool ? labelText(toolLabel(activeTool.name, activeTool.summary, ''), t) : ''
            return (
              <ToolGroup key={e.key} facts={facts} label={label} live duration={facts.total !== null ? fmt(facts.total) : ''}>
                {e.tools.map(toolRow)}
              </ToolGroup>
            )
          }
          return toolRow(e)
        })}
        {answer && renderText({ key: answerKey, text: answer, note: false })}
        {busy && <li className="cx-flow__wait" aria-hidden="true"><span className="cx-dots cx-dots--inline"><span /><span /><span /></span></li>}
      </ol>
      {consents.map(c => <CommandsConsentCard key={c.id} consent={c} />)}
    </>
  )
}
