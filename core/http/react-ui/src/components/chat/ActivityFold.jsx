import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { renderMarkdown, highlightAll } from '../../utils/markdown'
import { toolLine, toolOutput } from '../../utils/chatThread'
import { addonChange, toolRefusal } from '../../utils/addonChange'
import { proposalIds } from '../../utils/chatProposal'
import Icon from '../Icon'
// eslint-disable-next-line no-unused-vars
import ChangeNote from './ChangeNote'
// eslint-disable-next-line no-unused-vars
import ChatAppFrame from '../ChatAppFrame'
// eslint-disable-next-line no-unused-vars
import ProposalCard from './ProposalCard'

// What the thread says about reasoning and tool use: one quiet line that opens
// inline into the steps. Everything it shows is in the transcript the server
// keeps: reasoning lines and tool lines ("name: summary", then a capped output).

// "Thought · read_file", "Thought · 3 tool calls": what happened, in the fewest
// words that still say it.
function summarise(items, t) {
  const parts = []
  if (items.some(i => i.role === 'reasoning')) parts.push(t('activity.thought'))
  const calls = items.filter(i => i.role === 'tool')
  if (calls.length === 1) parts.push(toolLine(calls[0].text).name || t('activity.tool'))
  else if (calls.length > 1) parts.push(t('activity.toolCalls', { count: calls.length }))
  if (parts.length === 0) parts.push(t('activity.result'))
  return parts.join(' · ')
}

// eslint-disable-next-line no-unused-vars
function FoldFrame({ summary, open, live, shimmer, onToggle, icon, children, id }) {
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
        <span className={shimmer && !open ? 'cx-shimmer' : undefined}>{summary}</span>
        <Icon name="chevron-right" className="cx-fold__chev" />
      </button>
      {open && <div className="cx-fold__body" id={bodyId}>{children}</div>}
    </div>
  )
}

// One stored tool line as a step: its name, what it was called with and its
// output. The note of an add-on change (with Undo) is not in the fold: it shows
// under it, see ActivityGroup.
// eslint-disable-next-line no-unused-vars
function ToolStep({ text }) {
  const { t } = useTranslation('chat')
  const { name, summary } = toolLine(text)
  const out = toolOutput(text)
  return (
    <li className="cx-step">
      <h4>{t('activity.toolCall')} <code>{name || t('activity.tool')}</code></h4>
      {summary && <div className="cx-think">{summary}</div>}
      {out && !addonChange(text) && !toolRefusal(text) && <pre className="cx-out">{out}</pre>}
    </li>
  )
}

// One run of reasoning and tool lines between two messages. Add-on changes,
// refusals and trigger proposals are listed under the fold, so their buttons
// and reasons stay in sight.
export function ActivityGroup({ items, id, proposalTick = 0 }) {
  const { t } = useTranslation('chat')
  const { t: tc } = useTranslation('chats')
  const [expanded, setExpanded] = useState(false)
  const contentRef = useRef(null)

  useEffect(() => {
    if (expanded && contentRef.current) highlightAll(contentRef.current)
  }, [expanded])

  if (!items || items.length === 0) return null
  const changes = items.filter(item => item.role === 'tool' && (addonChange(item.text) || toolRefusal(item.text)))
  const proposals = proposalIds(items)
  const icon = items.some(i => i.role === 'reasoning') ? 'lightbulb' : 'wrench'

  return (
    <>
      <FoldFrame
        id={id}
        icon={icon}
        summary={summarise(items, t)}
        open={expanded}
        onToggle={() => setExpanded(v => !v)}
      >
        <ol className="cx-steps" ref={contentRef}>
          {items.map((item, idx) => (item.role === 'reasoning' ? (
            <li key={idx} className="cx-step">
              <h4>{t('activity.thought')}{item.dur_ms ? ` · ${Math.max(1, Math.round(item.dur_ms / 1000))} s` : ''}</h4>
              <div className="cx-think" dangerouslySetInnerHTML={{ __html: renderMarkdown(item.text || '') }} />
            </li>
          ) : <ToolStep key={idx} text={item.text} />))}
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
  const line = row.live
    ? `${tool.name}${tool.summary ? ` ${tool.summary}` : ''}`
    : toolLine(row.m.text).name
  const app = row.live ? (tool.appState === 'ready' ? tool.app : undefined) : row.m.app
  const loading = row.live && tool.appState === 'loading'
  const fallback = row.live && tool.appState === 'failed' ? (tool.output || '') : undefined
  return (
    <div className="cx-row" data-testid="chat-app-row">
      <div className="cx-who"><Icon name="puzzle" /><b>{line}</b></div>
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

// The same line while the reply is still coming: open while the model thinks
// or calls a tool, folded once the answer starts. The reader can override both.
// toolCalls come from streamingParts (chatThread.js).
export function StreamingActivity({ reasoning, toolCalls, hasResponse }) {
  const { t } = useTranslation('chat')
  const contentRef = useRef(null)
  const [manualCollapse, setManualCollapse] = useState(null)
  const hasTools = !!toolCalls && toolCalls.length > 0
  const autoExpanded = (!!reasoning || hasTools) && !hasResponse
  const expanded = manualCollapse !== null ? !manualCollapse : autoExpanded

  useEffect(() => {
    if (expanded && contentRef.current) contentRef.current.scrollTop = contentRef.current.scrollHeight
  }, [reasoning, expanded])

  useEffect(() => { setManualCollapse(null) }, [hasResponse])

  if (!reasoning && !hasTools) return null

  const lastTool = hasTools ? toolCalls[toolCalls.length - 1] : null
  const label = lastTool && (lastTool.type === 'tool_call' || !reasoning)
    ? (lastTool.type === 'tool_call' ? lastTool.name : t('activity.toolResult', { name: lastTool.name }))
    : (hasResponse ? t('activity.thought') : t('activity.thinking'))

  return (
    <FoldFrame
      id="cx-live-fold"
      icon={reasoning && (!lastTool || lastTool.type !== 'tool_call') ? 'lightbulb' : 'wrench'}
      summary={label}
      open={expanded}
      live
      shimmer={!hasResponse}
      onToggle={() => setManualCollapse(expanded)}
    >
      <ol className="cx-steps">
        {reasoning && (
          <li className="cx-step">
            <div className="cx-think cx-think--live" ref={contentRef} dangerouslySetInnerHTML={{ __html: renderMarkdown(reasoning) }} />
          </li>
        )}
        {hasTools && toolCalls.map((tc, idx) => (
          <li key={idx} className="cx-step" data-failed={tc.failed || undefined}>
            <h4>
              {tc.type === 'tool_call'
                ? <>{t('activity.toolCall')} <code>{tc.name}</code> <Icon name="spinner" spin /></>
                : t('activity.toolResult', { name: tc.name })}
            </h4>
            {tc.summary && <div className="cx-think">{tc.summary}</div>}
            {tc.result && <pre className="cx-out">{tc.result}</pre>}
          </li>
        ))}
      </ol>
    </FoldFrame>
  )
}
