import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { renderMarkdown, highlightAll } from '../../utils/markdown'
import { toolLine, toolOutput } from '../../utils/chatThread'
import { addonChange, toolRefusal } from '../../utils/addonChange'
import { proposalIds } from '../../utils/chatProposal'
import { toolLabel, formatDuration, runTimes } from '../../utils/toolLabels'
import Icon from '../Icon'
import { openChatFile } from './chatActions'
// eslint-disable-next-line no-unused-vars
import ChangeNote from './ChangeNote'
// eslint-disable-next-line no-unused-vars
import ChatAppFrame from '../ChatAppFrame'
// eslint-disable-next-line no-unused-vars
import ProposalCard from './ProposalCard'

// What the thread says about reasoning and tool use: one quiet line that opens
// inline into the steps. Everything it shows is in the transcript the server
// keeps: reasoning lines and tool lines ("name: summary", then a capped output).
//
// A step reads as a person would say it ("Read quote-acme.pdf", "Listed
// /Contracts · 3 files") with its time on the right. The tool id, its input and
// its output are one click away, for debugging.

// Output longer than this many lines shows its start and a "Show all" button.
const OUTPUT_LINES = 12

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
  const count = t('steps.count', { count: calls.length })
  return duration ? `${t('steps.worked', { duration })} · ${count}` : count
}

// A label as plain text, for the fold line.
function labelText(label, t) {
  const verb = t(`steps.verb.${label.verb}`)
  return [verb, label.verb === 'used' ? label.tool : '', label.arg].filter(Boolean).join(' ')
}

// eslint-disable-next-line no-unused-vars
function FoldFrame({ summary, open, live, working, onToggle, icon, children, id }) {
  const bodyId = `${id}-body`
  return (
    <div className="cx-fold" data-open={open || undefined} data-live={live || undefined} data-testid="chat-activity">
      <button
        type="button"
        className="cx-fold__head"
        aria-expanded={open}
        aria-controls={open ? bodyId : undefined}
        onClick={onToggle}
      >
        <Icon name={icon} />
        <span className="cx-fold__label">{summary}</span>
        {working && <span className="cx-dots cx-dots--inline" aria-hidden="true"><span /><span /><span /></span>}
        <Icon name="chevron-right" className="cx-fold__chev" />
      </button>
      {open && <div className="cx-fold__body" id={bodyId}>{children}</div>}
    </div>
  )
}

// The verb, the argument and what came back, as a person reads them.
// eslint-disable-next-line no-unused-vars
function ToolText({ label }) {
  const { t } = useTranslation('chat')
  return (
    <>
      <b className="cx-step__verb">{t(`steps.verb.${label.verb}`)}</b>
      {label.verb === 'used' && <code className="cx-step__tool">{label.tool || t('activity.tool')}</code>}
      {label.arg && (label.file ? (
        // The file a step saved opens in the Files pane. The step line is not
        // a button when it holds this one (see Step), so they do not nest.
        <button
          type="button" className="cx-step__file" title={t('steps.openFile', { path: label.argTitle })}
          data-testid="chat-step-file"
          onClick={(e) => { e.stopPropagation(); openChatFile(label.argTitle) }}
        >
          <Icon name="file-text" />{label.arg}
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
// the button for the keyboard.
// eslint-disable-next-line no-unused-vars
function Step({ state, duration, children, detail, defaultOpen = false, testId, split = false }) {
  const { t } = useTranslation('chat')
  const [open, setOpen] = useState(defaultOpen)
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
        <button type="button" className="cx-step__toggle" aria-expanded={open} aria-label={t('steps.details')} onClick={(e) => { e.stopPropagation(); setOpen(v => !v) }}>
          <Icon name="chevron-right" className="cx-step__chev" />
        </button>
      ) : detail ? <Icon name="chevron-right" className="cx-step__chev" /> : <span />}
    </>
  )
  return (
    <li className="cx-step" data-state={state} data-open={(detail && open) || undefined} data-testid={testId}>
      {detail && split ? (
        // A click anywhere on the line toggles; the chevron is the keyboard control.
        <div className="cx-step__head cx-step__head--split" onClick={() => setOpen(v => !v)}>{head}</div>
      ) : detail ? (
        <button type="button" className="cx-step__head" aria-expanded={open} onClick={() => setOpen(v => !v)}>{head}</button>
      ) : (
        <div className="cx-step__head">{head}</div>
      )}
      {detail && open && <div className="cx-step__detail">{detail}</div>}
    </li>
  )
}

// The first line of a reasoning text, without Markdown marks, for the step line.
function gist(text) {
  const first = String(text || '').trim().split('\n').find(l => l.trim()) || ''
  return first.replace(/[#*_`>]/g, '').replace(/\s+/g, ' ').trim()
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

// A reasoning line of the transcript or of the live turn.
// eslint-disable-next-line no-unused-vars
function ThoughtStep({ text, duration, live, thinking }) {
  const { t } = useTranslation('chat')
  const short = gist(text)
  const more = live || String(text || '').trim() !== short || short.length > 90
  return (
    <Step
      state="thought"
      duration={duration}
      defaultOpen={live}
      detail={more ? <Reasoning text={text} live={live} /> : null}
    >
      <b className="cx-step__verb">{thinking ? t('steps.thinking') : t('activity.thought')}</b>
      <span className="cx-step__gist">{short}</span>
    </Step>
  )
}

// One stored tool line as a step. The note of an add-on change (with Undo) is
// not in the fold: it shows under it, see ActivityGroup.
// eslint-disable-next-line no-unused-vars
function ToolStep({ text, duration }) {
  const { name, summary } = toolLine(text)
  const out = toolOutput(text)
  const refused = Boolean(toolRefusal(text))
  const shownOut = out && !addonChange(text) && !refused ? out : ''
  const label = toolLabel(name, summary, refused ? '' : out)
  return (
    <Step
      state={refused ? 'failed' : 'done'}
      duration={duration}
      split={label.file && Boolean(label.arg)}
      testId="chat-step"
      detail={<ToolDetail name={name} summary={summary} output={shownOut} />}
    >
      <ToolText label={label} />
    </Step>
  )
}

// One run of reasoning and tool lines between two messages. Add-on changes,
// refusals and trigger proposals are listed under the fold, so their buttons
// and reasons stay in sight.
export function ActivityGroup({ items, id, proposalTick = 0 }) {
  const { t, i18n } = useTranslation('chat')
  const { t: tc } = useTranslation('chats')
  const [expanded, setExpanded] = useState(false)

  if (!items || items.length === 0) return null
  const changes = items.filter(item => item.role === 'tool' && (addonChange(item.text) || toolRefusal(item.text)))
  const proposals = proposalIds(items)
  const icon = items.some(i => i.role === 'reasoning') ? 'lightbulb' : 'wrench'
  const { steps } = runTimes(items)
  const dur = (i) => (steps[i] !== null ? formatDuration(steps[i], t, i18n.language) : '')

  return (
    <>
      <FoldFrame
        id={id}
        icon={icon}
        summary={summarise(items, t, i18n.language)}
        open={expanded}
        onToggle={() => setExpanded(v => !v)}
      >
        <ol className="cx-steps">
          {items.map((item, idx) => (item.role === 'reasoning'
            ? <ThoughtStep key={idx} text={item.text} duration={dur(idx)} />
            : <ToolStep key={idx} text={item.text} duration={dur(idx)} />))}
        </ol>
      </FoldFrame>
      {changes.map((item) => <ChangeNote key={`c-${item.seq}`} text={item.text} t={tc} />)}
      {proposals.map((pid) => <ProposalCard key={`p-${pid}`} id={pid} t={tc} tick={proposalTick} />)}
    </>
  )
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

const now = () => (typeof performance !== 'undefined' ? performance.now() : Date.now())

// When each live step started and ended, as seen by this page. A step whose
// start and end arrive in the same render (a replay after a reconnect) has no
// time: what the page measured would not be how long it ran.
function useLiveTimes(reasoning, toolCalls, hasResponse) {
  const [times, setTimes] = useState({ reasoning: null, tools: [] })
  const thinking = Boolean(reasoning)
  const thoughtDone = thinking && (toolCalls.length > 0 || hasResponse)
  const ended = toolCalls.map(tc => tc.type !== 'tool_call')
  const key = `${thinking}|${thoughtDone}|${ended.join(',')}`
  useEffect(() => {
    const at = now()
    setTimes(prev => {
      let changed = false
      let r = prev.reasoning
      if (thinking && !r) { r = { start: at, end: thoughtDone ? null : undefined }; changed = true }
      else if (r && thoughtDone && r.end === undefined) { r = { ...r, end: at }; changed = true }
      const tools = ended.map((done, i) => {
        const p = prev.tools[i]
        if (!p) { changed = true; return { start: at, end: done ? null : undefined } }
        if (done && p.end === undefined) { changed = true; return { ...p, end: at } }
        return p
      })
      return changed ? { reasoning: r, tools } : prev
    })
    // key holds everything the effect reads
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])
  const span = (x) => (x && typeof x.end === 'number' ? x.end - x.start : null)
  const known = [times.reasoning, ...times.tools].filter(Boolean)
  const first = known[0]
  const last = known[known.length - 1]
  const total = first && last && typeof last.end === 'number' && known.every(x => x.end !== null)
    ? last.end - first.start
    : null
  return { reasoning: span(times.reasoning), tools: times.tools.map(span), total }
}

// The same line while the reply is still coming: open while the model thinks
// or calls a tool, folded once the answer starts. The reader can override both.
// toolCalls come from streamingParts (chatThread.js).
export function StreamingActivity({ reasoning, toolCalls, hasResponse }) {
  const { t, i18n } = useTranslation('chat')
  const [manualCollapse, setManualCollapse] = useState(null)
  const calls = toolCalls || []
  const hasTools = calls.length > 0
  const autoExpanded = (!!reasoning || hasTools) && !hasResponse
  const expanded = manualCollapse !== null ? !manualCollapse : autoExpanded
  const times = useLiveTimes(reasoning, calls, hasResponse)

  useEffect(() => { setManualCollapse(null) }, [hasResponse])

  if (!reasoning && !hasTools) return null

  const lng = i18n.language
  const fmt = (ms) => (ms !== null && ms !== undefined ? formatDuration(ms, t, lng) : '')
  const running = calls.some(tc => tc.type === 'tool_call')
  const count = hasTools ? t('steps.count', { count: calls.length }) : ''
  let summary
  if (!hasResponse) {
    summary = hasTools ? `${t('steps.working')} · ${count}` : t('steps.thinking')
  } else if (times.total !== null) {
    summary = hasTools ? `${t('steps.worked', { duration: fmt(times.total) })} · ${count}` : t('steps.thoughtFor', { duration: fmt(times.total) })
  } else {
    summary = hasTools ? count : t('activity.thought')
  }

  return (
    <FoldFrame
      id="cx-live-fold"
      icon={reasoning && !running ? 'lightbulb' : 'wrench'}
      summary={summary}
      open={expanded}
      live
      working={!hasResponse}
      onToggle={() => setManualCollapse(expanded)}
    >
      <ol className="cx-steps">
        {reasoning && (
          <ThoughtStep text={reasoning} duration={fmt(times.reasoning)} live thinking={!hasTools && !hasResponse} />
        )}
        {calls.map((tc, idx) => {
          const state = tc.type === 'tool_call' ? 'running' : (tc.failed ? 'failed' : 'done')
          const label = toolLabel(tc.name, tc.summary, tc.failed ? '' : tc.result)
          return (
            <Step
              key={idx}
              state={state}
              duration={state === 'running' ? '' : fmt(times.tools[idx])}
              testId="chat-step"
              split={label.file && Boolean(label.arg)}
              detail={<ToolDetail name={tc.name} summary={tc.summary} output={tc.result} />}
            >
              <ToolText label={label} />
            </Step>
          )
        })}
      </ol>
    </FoldFrame>
  )
}
