import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { renderMarkdown, highlightAll } from '../../utils/markdown'
// eslint-disable-next-line no-unused-vars
import MCPAppFrame from '../MCPAppFrame'
import Icon from '../Icon'

// What the thread says about reasoning and tool use: one quiet line that opens
// inline into the steps. Everything it shows is already in the history
// (thinking, tool_call and tool_result entries); nothing here is measured or
// timed, because the history carries no durations.

function formatToolContent(raw) {
  try {
    const data = JSON.parse(raw)
    const name = data.name || 'unknown'
    let params = data.arguments || data.input || data.result || data.parameters || {}
    if (typeof params === 'string') {
      try { params = JSON.parse(params) } catch { /* keep as string */ }
    }
    if (typeof params === 'string') return { name, entries: [], fallback: params }
    const entries = typeof params === 'object' && params !== null ? Object.entries(params) : []
    return { name, entries, fallback: null }
  } catch {
    return { name: null, entries: [], fallback: raw }
  }
}

// eslint-disable-next-line no-unused-vars
function ToolParams({ entries, fallback }) {
  if (fallback) return <pre className="cx-out">{fallback}</pre>
  if (entries.length === 0) return null
  return (
    <dl className="cx-params">
      {entries.map(([k, v]) => {
        const val = typeof v === 'string' ? v : JSON.stringify(v, null, 2)
        return (
          <div key={k} className="cx-param">
            <dt>{k}</dt>
            <dd className={val.length > 120 ? 'cx-param__long' : undefined}>{val}</dd>
          </div>
        )
      })}
    </dl>
  )
}

function callName(item, fallback) {
  try { return JSON.parse(item.content)?.name || fallback } catch { return fallback }
}

// "Thought · read_file", "Thought · 3 tool calls": what happened, in the fewest
// words that still say it.
function summarise(items, t) {
  const parts = []
  if (items.some(i => i.role === 'thinking' || i.role === 'reasoning')) parts.push(t('activity.thought'))
  const calls = items.filter(i => i.role === 'tool_call')
  if (calls.length === 1) parts.push(callName(calls[0], t('activity.tool')))
  else if (calls.length > 1) parts.push(t('activity.toolCalls', { count: calls.length }))
  if (parts.length === 0) parts.push(t('activity.result'))
  return parts.join(' · ')
}

// eslint-disable-next-line no-unused-vars
function FoldFrame({ summary, open, live, onToggle, icon, children, id }) {
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
        <span className={live && !open ? 'cx-shimmer' : undefined}>{summary}</span>
        <Icon name="chevron-right" className="cx-fold__chev" />
      </button>
      {open && <div className="cx-fold__body" id={bodyId}>{children}</div>}
    </div>
  )
}

// One run of thinking and tool entries between two messages.
export function ActivityGroup({ items, getClientForTool, id }) {
  const { t } = useTranslation('chat')
  const [expanded, setExpanded] = useState(false)
  const contentRef = useRef(null)

  useEffect(() => {
    if (expanded && contentRef.current) highlightAll(contentRef.current)
  }, [expanded])

  if (!items || items.length === 0) return null

  // A tool result that came with an app UI is shown on its own, not folded.
  const appUIItems = items.filter(item => item.role === 'tool_result' && item.appUI)
  const regularItems = items.filter(item => !(item.role === 'tool_result' && item.appUI))
  const icon = regularItems.some(i => i.role === 'thinking' || i.role === 'reasoning') ? 'lightbulb' : 'wrench'

  return (
    <>
      {regularItems.length > 0 && (
        <FoldFrame
          id={id}
          icon={icon}
          summary={summarise(regularItems, t)}
          open={expanded}
          onToggle={() => setExpanded(v => !v)}
        >
          <ol className="cx-steps" ref={contentRef}>
            {regularItems.map((item, idx) => {
              if (item.role === 'thinking' || item.role === 'reasoning') {
                return (
                  <li key={idx} className="cx-step">
                    <h4>{t('activity.thought')}</h4>
                    <div className="cx-think" dangerouslySetInnerHTML={{ __html: renderMarkdown(item.content || '') }} />
                  </li>
                )
              }
              const isCall = item.role === 'tool_call'
              const parsed = formatToolContent(item.content)
              const name = parsed.name || t('activity.tool')
              return (
                <li key={idx} className="cx-step">
                  <h4>
                    {isCall ? t('activity.toolCall') : t('activity.result')} <code>{name}</code>
                  </h4>
                  <ToolParams entries={parsed.entries} fallback={parsed.fallback} />
                </li>
              )
            })}
          </ol>
        </FoldFrame>
      )}
      {appUIItems.map((item, idx) => (
        <div key={`appui-${idx}`} className="cx-row">
          <div className="cx-who"><Icon name="puzzle" /><b>{item.appUI.toolName}</b></div>
          <MCPAppFrame
            toolName={item.appUI.toolName}
            toolInput={item.appUI.toolInput}
            toolResult={item.appUI.toolResult}
            mcpClient={getClientForTool?.(item.appUI.toolName) || null}
            toolDefinition={item.appUI.toolDefinition}
            appHtml={item.appUI.html}
            resourceMeta={item.appUI.meta}
          />
        </div>
      ))}
    </>
  )
}

// The same line while the reply is still coming: open while the model thinks
// or calls a tool, folded once the answer starts. The reader can override both.
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
  const label = reasoning
    ? t('activity.thinking')
    : (lastTool.type === 'tool_call' ? lastTool.name : t('activity.toolResult', { name: lastTool.name }))

  return (
    <FoldFrame
      id="cx-live-fold"
      icon={reasoning ? 'lightbulb' : 'wrench'}
      summary={label}
      open={expanded}
      live
      onToggle={() => setManualCollapse(expanded)}
    >
      <ol className="cx-steps">
        {reasoning && (
          <li className="cx-step">
            <div className="cx-think cx-think--live" ref={contentRef} dangerouslySetInnerHTML={{ __html: renderMarkdown(reasoning) }} />
          </li>
        )}
        {hasTools && toolCalls.map((tc, idx) => {
          if (tc.type === 'tool_result') {
            return (
              <li key={idx} className="cx-step">
                <h4>{t('activity.toolResult', { name: tc.name })}</h4>
                <div className="cx-think" dangerouslySetInnerHTML={{ __html: renderMarkdown(tc.result || '') }} />
              </li>
            )
          }
          const parsed = formatToolContent(JSON.stringify(tc, null, 2))
          return (
            <li key={idx} className="cx-step">
              <h4>{t('activity.toolCall')} <code>{tc.name || tc.type}</code></h4>
              <ToolParams entries={parsed.entries} fallback={parsed.fallback} />
            </li>
          )
        })}
      </ol>
    </FoldFrame>
  )
}
