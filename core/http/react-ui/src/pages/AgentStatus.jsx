import { useState, useEffect, useCallback, useMemo } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link, useParams, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { agentsApi } from '../utils/api'
import { apiUrl } from '../utils/basePath'
import { agentPath } from '../components/agents/AgentBits'
import Icon, { FaIcon } from '../components/Icon'
import './agents.css'

// The raw record of what an agent did, one entry per action, as the agent
// reports it. A run's report says what came of a task; this page is for
// reading the steps behind it.

// The lines an entry shows when folded: what started it, what it called, what
// came back and what went wrong. Each is a line the agent really reported.
function summaryLines(observable, t) {
  const creation = observable?.creation || {}
  const completion = observable?.completion || {}

  let creationMsg = ''
  if (creation?.chat_completion_message?.content) {
    creationMsg = creation.chat_completion_message.content
  } else {
    const messages = creation?.chat_completion_request?.messages
    if (Array.isArray(messages) && messages.length > 0) {
      creationMsg = messages[messages.length - 1]?.content || ''
    }
  }
  if (typeof creationMsg === 'object') creationMsg = t('status.multimedia')

  const funcDef = creation?.function_definition?.name ? t('status.function', { name: creation.function_definition.name }) : ''
  const funcParams = creation?.function_params && Object.keys(creation.function_params).length > 0
    ? t('status.params', { params: JSON.stringify(creation.function_params) }) : ''

  let completionMsg = ''
  let toolCallSummary = ''
  let chatCompletion = completion?.chat_completion_response
  if (!chatCompletion && Array.isArray(completion?.conversation) && completion.conversation.length > 0) {
    chatCompletion = { choices: completion.conversation.map(m => ({ message: m })) }
  }
  if (chatCompletion?.choices?.length > 0) {
    const last = chatCompletion.choices[chatCompletion.choices.length - 1]
    const toolCalls = last?.message?.tool_calls
    if (Array.isArray(toolCalls) && toolCalls.length > 0) {
      toolCallSummary = toolCalls.map(tc => {
        const args = tc.function?.arguments || ''
        return `${tc.function?.name || 'unknown'}(${typeof args === 'string' ? args : JSON.stringify(args)})`
      }).join(', ')
    }
    completionMsg = last?.message?.content || ''
  }

  const actionResult = completion?.action_result ? String(completion.action_result).slice(0, 100) : ''
  const errorMsg = completion?.error || ''
  let filterInfo = ''
  if (completion?.filter_result) {
    const fr = completion.filter_result
    if (fr.has_triggers && !fr.triggered_by) filterInfo = t('status.noTrigger')
    else if (fr.triggered_by) filterInfo = t('status.triggeredBy', { name: fr.triggered_by })
    if (fr.failed_by) filterInfo += `${filterInfo ? ', ' : ''}${t('status.failedBy', { name: fr.failed_by })}`
  }

  const items = []
  if (creationMsg) items.push({ text: creationMsg })
  if (funcDef) items.push({ text: funcDef })
  if (funcParams) items.push({ text: funcParams })
  if (toolCallSummary) items.push({ text: toolCallSummary })
  if (completionMsg) items.push({ text: completionMsg })
  if (actionResult) items.push({ text: actionResult })
  if (errorMsg) items.push({ text: errorMsg, kind: 'error' })
  if (filterInfo) items.push({ text: filterInfo })
  return items
}

// eslint-disable-next-line no-unused-vars
function ObservableItem({ observable, children }) {
  const { t } = useTranslation('agents')
  const [expanded, setExpanded] = useState(false)
  const done = !!observable.completion
  const failed = !!observable.completion?.error
  const hasProgress = observable.progress?.length > 0
  const lines = summaryLines(observable, t)
  const state = !done ? 'running' : failed ? 'failed' : 'done'

  return (
    <div className="ag-obs__item" data-testid="status-observable">
      <button type="button" className="ag-obs__head" aria-expanded={expanded} onClick={() => setExpanded(v => !v)}>
        <span className="ag-obs__icon"><FaIcon name={observable.icon || 'robot'} /></span>
        <span>
          <span className="ag-obs__name">
            {observable.name}
            <span className="ag-obs__id">#{observable.id}</span>
          </span>
          {lines.length > 0 && (
            <span className="ag-obs__sum">
              {lines.slice(0, 2).map((l, i) => <span key={i} data-kind={l.kind} title={l.text}>{l.text}</span>)}
            </span>
          )}
        </span>
        <span className="ag-state" data-state={state}>
          <span className={`dk-dot${state === 'running' ? ' dk-dot--accent' : state === 'failed' ? ' dk-dot--error' : ' dk-dot--ok'}`} aria-hidden="true" />
          {t(`state.${state === 'done' ? 'done' : state}`)}
        </span>
        <Icon name={expanded ? 'chevron-up' : 'chevron-down'} />
      </button>

      {expanded && (
        <div className="ag-obs__body">
          {children && children.length > 0 && (
            <div>
              <p className="ag-eyebrow ag-obs__label">{t('status.nested')}</p>
              {children}
            </div>
          )}

          {hasProgress && (
            <div>
              <p className="ag-eyebrow ag-obs__label">{t('status.progress', { count: observable.progress.length })}</p>
              {observable.progress.map((p, i) => (
                <div key={i} className="ag-obs__entry">
                  {p.action_result && <div><span className="ag-obs__tag">{t('status.actionResult')}</span>{p.action_result}</div>}
                  {p.error && <div><span className="ag-obs__tag" data-kind="error">{t('status.error')}</span>{p.error}</div>}
                  {p.chat_completion_response?.choices?.length > 0 && (
                    <div>
                      <span className="ag-obs__tag">{t('status.response')}</span>
                      {p.chat_completion_response.choices.map((ch, ci) => (
                        <span key={ci}>{ch.message?.content || t('status.toolCall')}</span>
                      ))}
                    </div>
                  )}
                  {p.agent_state && (
                    <div><span className="ag-obs__tag">{t('status.state')}</span>{JSON.stringify(p.agent_state)}</div>
                  )}
                </div>
              ))}
            </div>
          )}

          {observable.completion && (
            <div>
              <p className="ag-eyebrow ag-obs__label">{t('status.completion')}</p>
              {observable.completion.action_result && (
                <div className="ag-obs__entry"><span className="ag-obs__tag">{t('status.actionResult')}</span>{observable.completion.action_result}</div>
              )}
              {observable.completion.error && (
                <div className="ag-obs__entry"><span className="ag-obs__tag" data-kind="error">{t('status.error')}</span>{observable.completion.error}</div>
              )}
              {observable.completion.filter_result && (
                <div className="ag-obs__entry"><span className="ag-obs__tag">{t('status.filter')}</span>{JSON.stringify(observable.completion.filter_result)}</div>
              )}
            </div>
          )}

          <details>
            <summary className="ag-muted">{t('status.rawJson')}</summary>
            <pre className="ag-obs__json">{JSON.stringify(observable, null, 2)}</pre>
          </details>
        </div>
      )}
    </div>
  )
}

function buildTree(observables) {
  const byId = {}
  observables.forEach(obs => { byId[obs.id] = { ...obs, children: [] } })
  const roots = []
  observables.forEach(obs => {
    if (obs.parent_id && byId[obs.parent_id]) {
      byId[obs.parent_id].children.push(byId[obs.id])
    } else {
      roots.push(byId[obs.id])
    }
  })
  return roots
}

function renderTree(nodes) {
  return nodes.map(node => (
    <ObservableItem key={node.id} observable={node}>
      {node.children.length > 0 ? renderTree(node.children) : null}
    </ObservableItem>
  ))
}

export default function AgentStatus() {
  const { name } = useParams()
  const { t } = useTranslation('agents')
  const { addToast } = useOutletContext()
  const [searchParams] = useSearchParams()
  const userId = searchParams.get('user_id') || undefined
  const [observables, setObservables] = useState([])
  const [status, setStatus] = useState(null)
  const [loading, setLoading] = useState(true)
  const [live, setLive] = useState(false)
  const [only, setOnly] = useState('all')

  const fetchData = useCallback(async () => {
    try {
      const obsData = await agentsApi.observables(name, userId)
      const history = Array.isArray(obsData) ? obsData : (obsData?.History || [])
      setObservables(history)
    } catch (err) {
      addToast(t('status.loadFailed', { message: err.message }), 'error')
    }
    try {
      const statusData = await agentsApi.status(name, userId)
      setStatus(statusData)
    } catch (_) {
      // status endpoint may fail if no actions have run yet
    }
    setLoading(false)
  }, [name, userId, addToast, t])

  useEffect(() => {
    fetchData()
    const interval = setInterval(fetchData, 5000)
    return () => clearInterval(interval)
  }, [fetchData])

  // SSE for real-time observable updates
  useEffect(() => {
    const url = apiUrl(agentsApi.sseUrl(name, userId))
    const es = new EventSource(url)

    es.onopen = () => setLive(true)
    es.addEventListener('observable_update', (e) => {
      try {
        const data = JSON.parse(e.data)
        setObservables(prev => {
          const idx = prev.findIndex(o => o.id === data.id)
          if (idx >= 0) {
            const updated = [...prev]
            const existing = updated[idx]
            updated[idx] = {
              ...existing,
              ...data,
              creation: data.creation || existing.creation,
              completion: data.completion || existing.completion,
              progress: (data.progress?.length ?? 0) > (existing.progress?.length ?? 0) ? data.progress : existing.progress,
            }
            return updated
          }
          return [...prev, data]
        })
      } catch (_) { /* ignore */ }
    })

    es.onerror = () => setLive(false) // reconnect is handled by the browser
    return () => es.close()
  }, [name, userId])

  const handleClear = async () => {
    try {
      await agentsApi.clearObservables(name, userId)
      setObservables([])
      addToast(t('status.cleared'), 'success')
    } catch (err) {
      addToast(t('status.clearFailed', { message: err.message }), 'error')
    }
  }

  const failedCount = observables.filter(o => o.completion?.error).length
  const shown = useMemo(
    () => (only === 'failed' ? observables.filter(o => o.completion?.error) : observables),
    [observables, only],
  )
  const tree = buildTree(shown)

  return (
    <div className="page page--medium ag-page" data-testid="agent-status">
      <div className="ag-status">
        <div className="ag-bar">
          <Link className="ag-back" to={agentPath(name, userId)}><Icon name="arrow-left" /> {name}</Link>
          <div className="ag-bar__acts">
            <span className="ag-state" data-state={live ? 'done' : undefined}>
              <span className={`dk-dot${live ? ' dk-dot--ok' : ''}`} aria-hidden="true" />
              {live ? t('status.live') : t('status.notLive')}
            </span>
            <button className="btn btn-secondary btn-sm" onClick={fetchData}>
              <Icon name="refresh" /> {t('status.refresh')}
            </button>
            <button className="btn btn-danger btn-sm" onClick={handleClear} disabled={observables.length === 0}>
              <Icon name="trash" /> {t('status.clear')}
            </button>
          </div>
        </div>

        <header>
          <div className="ag-title"><h1>{t('status.title')}</h1></div>
          <p className="ag-lede">{t('status.lede', { name })}</p>
        </header>

        <dl className="ag-facts ag-status__facts">
          <dt>{t('status.stateLabel')}</dt>
          <dd>{status?.state || <span className="ag-muted">{t('status.unknown')}</span>}</dd>
          <dt>{t('status.currentTask')}</dt>
          <dd className="ag-facts__text">{status?.current_task || <span className="ag-muted">{t('chips.none')}</span>}</dd>
          <dt>{t('status.records')}</dt>
          <dd className="ag-facts__model">{t('status.recordsValue', { count: observables.length, failed: failedCount })}</dd>
        </dl>

        {loading ? (
          <div className="loading-center">
            <Icon name="spinner" spin className="icon-xl text-primary" />
          </div>
        ) : observables.length === 0 ? (
          <div className="ag-empty">
            <h3>{t('status.emptyTitle')}</h3>
            <p>{t('status.emptyText')}</p>
            <Link className="btn btn-primary" to={agentPath(name, userId)}>
              <Icon name="play" /> {t('status.giveTask', { name })}
            </Link>
          </div>
        ) : (
          <div>
            <div className="ag-tools">
              <div className="dk-segmented" role="group" aria-label={t('status.filterLabel')}>
                <button type="button" className="dk-seg" aria-pressed={only === 'all'} aria-selected={only === 'all'} onClick={() => setOnly('all')}>{t('status.all')}</button>
                <button type="button" className="dk-seg" aria-pressed={only === 'failed'} aria-selected={only === 'failed'} onClick={() => setOnly('failed')}>{t('status.failedOnly')}</button>
              </div>
            </div>
            {tree.length === 0
              ? <p className="ag-note">{t('status.noFailed')}</p>
              : <div className="ag-obs">{renderTree(tree)}</div>}
          </div>
        )}
      </div>
    </div>
  )
}
