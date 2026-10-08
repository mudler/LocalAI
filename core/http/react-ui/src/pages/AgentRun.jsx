import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link, useLocation, useNavigate, useOutletContext, useParams, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { agentsApi } from '../utils/api'
import { apiUrl } from '../utils/basePath'
import { copyToClipboard } from '../utils/clipboard'
import { agentInfo } from '../utils/agentInfo'
import {
  applyStatusLine, applyStreamEvent, durationMs, effectiveStatus, failTurn, finishTurn, firstLine, formatDuration,
  historyForRun, lastTurn, loadRun, newRun, newRunId, newTurn, saveRun, stepCount, titleOf,
} from '../utils/agentRuns'
// eslint-disable-next-line no-unused-vars
import { StatusMark, Chip, LibraryChips, formatWhen, agentPath } from '../components/agents/AgentBits'
// eslint-disable-next-line no-unused-vars
import Prose from '../components/agents/Prose'
// eslint-disable-next-line no-unused-vars
import ActionMenu from '../components/ActionMenu'
import Icon from '../components/Icon'
import './chat.css'
import './agents.css'

const SETTLE_MS = 1500

// ---- Small parts ---------------------------------------------------------

function resourcesOf(metadata, agent, userId) {
  const out = []
  Object.values(metadata || {}).forEach((values) => {
    if (!Array.isArray(values)) return
    values.forEach((v) => {
      if (typeof v !== 'string' || !v) return
      const web = /^https?:\/\//.test(v)
      let label = v.split('/').pop() || v
      if (web) { try { label = new URL(v).hostname } catch (_e) { label = v } }
      const href = web ? v : apiUrl(`/api/agents/${encodeURIComponent(agent)}/files?path=${encodeURIComponent(v)}${userId ? `&user_id=${encodeURIComponent(userId)}` : ''}`)
      out.push({ href, label, web })
    })
  })
  return out
}

// eslint-disable-next-line no-unused-vars
function Resources({ metadata, agent, userId }) {
  const items = resourcesOf(metadata, agent, userId)
  if (items.length === 0) return null
  return (
    <div className="ag-chips" data-testid="run-resources">
      {items.map((r, i) => (
        <a key={i} className="ag-chip ag-chip--link" href={r.href} target="_blank" rel="noopener noreferrer">
          <Icon name={r.web ? 'link' : 'file'} />
          <span className="ag-chip__text">{r.label}</span>
        </a>
      ))}
    </div>
  )
}

// eslint-disable-next-line no-unused-vars
function AgentSwitcher({ name, userId }) {
  const { t } = useTranslation('agents')
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const [names, setNames] = useState(null)
  const ref = useRef(null)

  useEffect(() => {
    if (!open) return
    agentsApi.list().then(d => setNames(Array.isArray(d.agents) ? d.agents : [])).catch(() => setNames([]))
    const onDown = (e) => { if (ref.current && !ref.current.contains(e.target)) setOpen(false) }
    const onKey = (e) => { if (e.key === 'Escape') setOpen(false) }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => { document.removeEventListener('mousedown', onDown); document.removeEventListener('keydown', onKey) }
  }, [open])

  return (
    <div className="ag-switch-wrap" ref={ref}>
      <button type="button" className="ag-switch" aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(v => !v)} data-testid="agent-switcher">
        {name}
        <Icon name="chevron-down" />
      </button>
      {open && (
        <div className="dk-menu dk-popover ag-switch__menu" role="menu" aria-label={t('run.switchAgent')}>
          {(names || []).map(n => (
            <button
              key={n}
              type="button"
              role="menuitem"
              className="dk-menu-item"
              aria-current={n === name ? 'true' : undefined}
              onClick={() => { setOpen(false); navigate(agentPath(n, userId)) }}
            >
              {n}
              {n === name && <Icon name="check" />}
            </button>
          ))}
          {names && names.length === 0 && <span className="dk-menu-label ag-muted">{t('run.noOtherAgents')}</span>}
        </div>
      )}
    </div>
  )
}

function stepLabel(step, t) {
  if (step.kind === 'tool') return <code>{step.name}</code>
  if (step.kind === 'thought') return t('run.thought')
  return t('run.statusLine')
}

function stepNote(step) {
  if (step.kind === 'tool') return firstLine(step.args, 120)
  return firstLine(step.text, 120)
}

// "Worked 26 s, 5 steps" folded to one line; opens into the steps.
// eslint-disable-next-line no-unused-vars
function Fold({ turn, running, now }) {
  const { t } = useTranslation('agents')
  const [open, setOpen] = useState(false)
  const steps = turn.steps
  if (steps.length === 0 && !running) return null
  const ms = running ? now - turn.startedAt : durationMs(turn)
  const count = stepCount(turn)
  const label = running
    ? t('run.workingFor', { time: formatDuration(ms), count })
    : turn.status === 'failed'
      ? t('run.stoppedAfter', { time: formatDuration(ms), count })
      : t('run.workedFor', { time: formatDuration(ms), count })
  return (
    <div className="cx-fold ag-fold-live" data-open={open || undefined} data-live={running || undefined} data-testid="run-fold">
      <button type="button" className="cx-fold__head ag-fold__head" aria-expanded={open} onClick={() => setOpen(v => !v)}>
        <Icon name="wrench" />
        <span className={running && !open ? 'cx-shimmer' : undefined}>{label}</span>
        <Icon name="chevron-right" className="cx-fold__chev" />
      </button>
      {open && (
        <ol className="cx-steps">
          {steps.length === 0 && <li className="cx-step"><h4>{t('run.noSteps')}</h4></li>}
          {steps.map((s, i) => (
            <li key={i} className="cx-step">
              <h4>{stepLabel(s, t)}</h4>
              {s.kind === 'tool' && s.args && <div className="ag-ev__args">{firstLine(s.args, 240)}</div>}
              {s.kind === 'thought' && <div className="cx-think"><Prose text={s.text} /></div>}
              {s.kind === 'status' && <div className="cx-think">{s.text}</div>}
              {s.kind === 'tool' && s.result && <pre className="cx-out">{s.result}</pre>}
            </li>
          ))}
        </ol>
      )}
    </div>
  )
}

// eslint-disable-next-line no-unused-vars
function FailPanel({ turn, onAgain, statusHref, compact = false }) {
  const { t } = useTranslation('agents')
  const sent = turn.errorKind === 'send'
  return (
    <div className="ag-fail" role="alert" data-testid="run-failed">
      <h3><Icon name="warning" /> {sent ? t('run.failSendTitle') : t('run.failTitle')}</h3>
      <p>{sent ? t('run.failSendText') : t('run.failText')}</p>
      {turn.error && <p className="ag-fail__why">{turn.error}</p>}
      <div className="ag-fail__acts">
        {onAgain && <button type="button" className="btn btn-primary" onClick={onAgain}><Icon name="refresh" /> {t('run.runAgain')}</button>}
        {!compact && statusHref && <Link className="ag-link" to={statusHref}>{t('run.seeStatus')}</Link>}
      </div>
    </div>
  )
}

// ---- Page --------------------------------------------------------------

export default function AgentRun() {
  const { name, id } = useParams()
  return <AgentRunInner key={`${name}/${id}`} name={name} id={id} />
}

// eslint-disable-next-line no-unused-vars
function AgentRunInner({ name, id }) {
  const { t } = useTranslation('agents')
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const location = useLocation()
  const [searchParams] = useSearchParams()
  const userId = searchParams.get('user_id') || undefined

  const startTask = location.state?.task
  const [run, setRun] = useState(() => loadRun(name, id, userId))
  const runRef = useRef(run)
  const saveTimer = useRef(null)
  const startedRef = useRef(false)
  const [config, setConfig] = useState(null)
  const [now, setNow] = useState(() => Date.now())
  const [view, setView] = useState(() => (run && effectiveStatus(run) !== 'running' ? 'report' : 'live'))
  const [pickedView, setPickedView] = useState(false)
  const [settling, setSettling] = useState(false)
  const [reconnecting, setReconnecting] = useState(false)
  const [followUp, setFollowUp] = useState('')
  const bodyRef = useRef(null)
  const stickRef = useRef(true)

  // The one place the run changes: keep the ref current for the stream
  // handlers, render it, and write it to the log shortly after.
  const commit = useCallback((update, { flush = false } = {}) => {
    const next = update(runRef.current)
    if (!next || next === runRef.current) return
    runRef.current = next
    setRun(next)
    clearTimeout(saveTimer.current)
    if (flush) saveRun(next)
    else saveTimer.current = setTimeout(() => saveRun(runRef.current), 400)
  }, [])

  useEffect(() => () => {
    clearTimeout(saveTimer.current)
    if (runRef.current) saveRun(runRef.current)
  }, [])

  const updateLast = useCallback((fn, opts) => {
    commit((r) => {
      if (!r) return r
      const turns = [...r.turns]
      const last = turns[turns.length - 1]
      const next = fn(last)
      if (next === last) return r
      turns[turns.length - 1] = next
      return { ...r, turns }
    }, opts)
  }, [commit])

  // Send a task to the agent: the first one starts the run, later ones are
  // follow-ups carrying the turns before them as history.
  const dispatch = useCallback(async (task, history) => {
    try {
      await agentsApi.chat(name, task, userId, history)
    } catch (err) {
      updateLast(turn => failTurn(turn, err.message, Date.now(), 'send'), { flush: true })
    }
  }, [name, userId, updateLast])

  // A run opened with a task and no record is a new run: write it, then send.
  useEffect(() => {
    if (startedRef.current || runRef.current || !startTask) return
    startedRef.current = true
    const fresh = newRun(name, startTask, { userId, id })
    runRef.current = fresh
    setRun(fresh)
    saveRun(fresh)
    dispatch(startTask, [])
  }, [startTask, name, userId, id, dispatch])

  // The agent's event stream. One run is one task until the agent answers.
  useEffect(() => {
    const es = new EventSource(apiUrl(agentsApi.sseUrl(name, userId)))
    const running = () => runRef.current && lastTurn(runRef.current)?.status === 'running'
    const parse = (e) => { try { return JSON.parse(e.data) } catch (_err) { return null } }

    es.onopen = () => setReconnecting(false)
    es.addEventListener('json_message', (e) => {
      const data = parse(e)
      if (!data || !running()) return
      const sender = data.sender || (data.role === 'user' ? 'user' : 'agent')
      if (sender === 'user') return
      const content = data.content || data.message || ''
      const metadata = data.metadata && Object.keys(data.metadata).length ? data.metadata : null
      updateLast(turn => finishTurn(turn, { content, metadata }), { flush: true })
    })
    es.addEventListener('stream_event', (e) => {
      const data = parse(e)
      if (data && running()) updateLast(turn => applyStreamEvent(turn, data))
    })
    es.addEventListener('status', (e) => {
      if (e.data && running()) updateLast(turn => applyStatusLine(turn, e.data))
    })
    es.addEventListener('json_error', (e) => {
      const data = parse(e)
      if (running()) updateLast(turn => failTurn(turn, data?.error || data?.message || ''), { flush: true })
    })
    es.onerror = () => setReconnecting(true)
    return () => es.close()
  }, [name, userId, updateLast])

  // Chips and the model name come from the saved config. Not needed to read
  // the run, so a missing agent just leaves them out.
  useEffect(() => {
    agentsApi.getConfig(name, userId).then(setConfig).catch(() => setConfig(null))
  }, [name, userId])

  const turn = run ? lastTurn(run) : null
  const status = run ? effectiveStatus(run, now) : 'stopped'
  const running = status === 'running'

  useEffect(() => {
    if (!running) return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [running])

  // When the agent answers, wait a moment and settle into the report, unless
  // the reader chose a view or the run failed (failure stays where it was seen).
  const wasRunning = useRef(running)
  useEffect(() => {
    if (wasRunning.current && !running && status === 'done' && !pickedView) {
      setSettling(true)
      const timer = setTimeout(() => { setSettling(false); setView('report') }, SETTLE_MS)
      wasRunning.current = running
      return () => { clearTimeout(timer); setSettling(false) }
    }
    wasRunning.current = running
  }, [running, status, pickedView])

  // Keep the live thread at its end unless the reader scrolled up.
  useEffect(() => {
    const el = bodyRef.current
    if (!el) return
    const onScroll = () => { stickRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80 }
    el.addEventListener('scroll', onScroll, { passive: true })
    return () => el.removeEventListener('scroll', onScroll)
  }, [])
  useEffect(() => {
    if (view === 'live' && stickRef.current && bodyRef.current) bodyRef.current.scrollTop = bodyRef.current.scrollHeight
  }, [run, view])

  const pick = (v) => { setPickedView(true); setSettling(false); setView(v) }

  const sendFollowUp = () => {
    const text = followUp.trim()
    if (!text || !runRef.current || running) return
    const history = historyForRun(runRef.current)
    setFollowUp('')
    commit((r) => ({ ...r, turns: [...r.turns, newTurn(text)] }), { flush: true })
    setPickedView(false)
    setView('live')
    dispatch(text, history)
  }

  const runAgain = (task) => navigate(agentPath(name, userId, `/runs/${newRunId()}`), { state: { task } })

  const copyLink = async () => {
    const ok = await copyToClipboard(`${window.location.origin}${window.location.pathname}`)
    addToast(t(ok ? 'run.linkCopied' : 'run.linkNotCopied'), ok ? 'success' : 'error', 2000)
  }

  const statusHref = agentPath(name, userId, '/status')
  const info = useMemo(() => agentInfo(config), [config])

  if (!run) {
    return (
      <div className="cx-page ag-page">
        <div className="ag-run">
          <div className="ag-body">
            <div className="ag-doc"><div className="ag-doc__in">
              <div className="ag-empty" data-testid="run-missing">
                <h1 className="ag-doc__title">{t('run.missingTitle')}</h1>
                <p>{t('run.missingText', { id })}</p>
                <div className="ag-empty__acts">
                  <Link className="btn btn-primary" to={agentPath(name, userId)}>{t('run.openAgent', { name })}</Link>
                  <Link className="btn btn-secondary" to="/app/agents">{t('agent.back')}</Link>
                </div>
              </div>
            </div></div>
          </div>
        </div>
      </div>
    )
  }

  const first = run.turns[0]
  const lastMs = turn.endedAt ? turn.endedAt - first.startedAt : null
  const elapsed = running ? now - turn.startedAt : lastMs
  const canFollowUp = !running
  const followUps = run.turns.slice(1)
  const evidence = run.turns.flatMap((tn, ti) => tn.steps.filter(s => s.kind === 'tool' && s.result).map(s => ({ ...s, turn: ti })))

  const dock = canFollowUp ? (
    <div className="ag-dock">
      <div className="ag-dock__in">
        <div className="dk-composer" data-testid="run-followup">
          <label className="dk-sr-only" htmlFor="ag-followup">{t('run.followUpLabel')}</label>
          <textarea
            id="ag-followup"
            className="dk-composer-input"
            rows={1}
            value={followUp}
            placeholder={status === 'done' ? t('run.followUpPlaceholder') : t('run.followUpPlaceholderStopped')}
            onChange={(e) => setFollowUp(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey && !e.nativeEvent?.isComposing && e.keyCode !== 229) {
                e.preventDefault()
                sendFollowUp()
              }
            }}
          />
          <div className="dk-composer-bar">
            <span className="dk-composer-hint">{t('run.followUpHint')}</span>
            <button type="button" className="dk-send" aria-label={t('run.send')} disabled={!followUp.trim()} onClick={sendFollowUp}>
              <Icon name="send" />
            </button>
          </div>
        </div>
      </div>
    </div>
  ) : (
    <div className="ag-dock">
      <div className="ag-dock__in">
        <p className="ag-dock__note" data-testid="run-dock-note">
          {reconnecting ? t('run.reconnecting') : t('run.dockWorking')}
        </p>
      </div>
    </div>
  )

  return (
    <div className="cx-page ag-page" data-testid="agent-run" data-status={status}>
      <div className="ag-run">
        <header className="ag-run__head">
          <Link className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" to={agentPath(name, userId)} aria-label={t('run.openAgent', { name })} title={t('run.openAgent', { name })}>
            <Icon name="arrow-left" />
          </Link>
          <div className="ag-run__title">
            <div className="ag-run__who">
              <AgentSwitcher name={name} userId={userId} />
              <span className="ag-run__task">{firstLine(first.task, 70)}</span>
            </div>
            <span className="ag-run__status">
              <StatusMark status={status} />
              {elapsed != null && <span className="ag-time">{formatDuration(elapsed)}</span>}
            </span>
          </div>
          <div className="ag-run__acts">
            <div className="dk-segmented" role="group" aria-label={t('run.view')}>
              <button type="button" className="dk-seg" aria-pressed={view === 'live'} aria-selected={view === 'live'} onClick={() => pick('live')}>{t('run.live')}</button>
              <button type="button" className="dk-seg" aria-pressed={view === 'report'} aria-selected={view === 'report'} onClick={() => pick('report')}>{t('run.report')}</button>
            </div>
            <a
              className="dk-btn dk-btn--ghost dk-btn--sm ag-hide-phone"
              href={statusHref}
              target="_blank"
              rel="noopener noreferrer"
              title={t('run.statusTitle')}
            >
              <Icon name="chart-bar" /> {t('agent.status')}
            </a>
            <ActionMenu
              ariaLabel={t('run.more')}
              items={[
                { key: 'copy', icon: 'link', label: t('run.copyLink'), onClick: copyLink },
                { key: 'reuse', icon: 'copy', label: t('run.reuseTask'), onClick: () => navigate(agentPath(name, userId), { state: { task: first.task } }) },
                { key: 'agent', icon: 'robot', label: t('run.openAgent', { name }), onClick: () => navigate(agentPath(name, userId)) },
              ]}
            />
          </div>
        </header>

        {view === 'live' ? (
          <div className="ag-body" ref={bodyRef} data-testid="run-live">
            <div className="ag-live"><div className="ag-live__in">
              {run.turns.map((tn, ti) => {
                const isLast = ti === run.turns.length - 1
                const tnRunning = isLast && running
                const tnFailed = tn.status === 'failed'
                return (
                  <div key={ti} className="cx-row ag-turn">
                    <div className="ag-task-card" data-testid="run-task">
                      <span className="ag-eyebrow">{ti === 0 ? t('run.task') : t('run.followUp')}</span>
                      {tn.task}
                    </div>
                    <Fold turn={tn} running={tnRunning} now={now} />
                    {tnRunning && (
                      <>
                        {tn.live.content
                          ? <Prose text={tn.live.content} live />
                          : (
                            <div className="ag-working" data-testid="run-working">
                              <Icon name="spinner" spin />
                              <span>{liveLine(tn, t)}</span>
                            </div>
                          )}
                      </>
                    )}
                    {tn.status === 'done' && (
                      <>
                        <Prose text={tn.outcome} />
                        <Resources metadata={tn.metadata} agent={name} userId={userId} />
                      </>
                    )}
                    {tnFailed && <FailPanel turn={tn} onAgain={isLast ? () => runAgain(first.task) : undefined} statusHref={statusHref} />}
                    {tn.status === 'running' && !tnRunning && (
                      <p className="ag-note" data-testid="run-cutoff">{t('run.cutOff')}</p>
                    )}
                  </div>
                )
              })}
              {settling && <p className="ag-ready-note" data-testid="run-settling"><Icon name="check" /> {t('run.reportReady')}</p>}
            </div></div>
          </div>
        ) : (
          <div className="ag-body" ref={bodyRef} data-testid="run-report">
            <article className="ag-doc"><div className="ag-doc__in">
              <header>
                <h1 className="ag-doc__title">{titleOf(first.task)}</h1>
                <div className="ag-doc__meta">
                  <StatusMark status={status} />
                  <Link to={agentPath(name, userId)}>{name}</Link>
                  <span>{t('run.started', { when: formatWhen(first.startedAt) })}</span>
                  {durationMs(first) != null && !run.legacy && <span>{formatDuration(durationMs(first))}</span>}
                  <span className="ag-mono">{run.id}</span>
                </div>
                <div className="ag-doc__chips">
                  <span className="ag-addr">
                    <span className="ag-addr__text">{`${window.location.pathname}`}</span>
                    <button type="button" className="ag-btn-quiet" onClick={copyLink}>{t('run.copyLink')}</button>
                  </span>
                  {info.model && <Chip mono title={t('facts.model')}>{info.model}</Chip>}
                  {info.memory.length > 0 && <LibraryChips info={info} kind="memory" />}
                  {info.skills.length > 0 && <LibraryChips info={info} kind="skills" />}
                </div>
                <p className="ag-note ag-note--gap">{t('run.addressNote')}</p>
              </header>

              <section className="ag-sec" aria-labelledby="ag-s-task">
                <div className="ag-sec__head"><h2 id="ag-s-task">{t('run.task')}</h2></div>
                <p className="ag-sec__text">{first.task}</p>
                <div className="ag-sec__acts">
                  <button type="button" className="ag-btn-quiet" onClick={() => navigate(agentPath(name, userId), { state: { task: first.task } })}>
                    <Icon name="copy" /> {t('run.reuseTask')}
                  </button>
                </div>
              </section>

              <section className="ag-sec" aria-labelledby="ag-s-out">
                <div className="ag-sec__head"><h2 id="ag-s-out">{t('run.outcome')}</h2></div>
                {first.status === 'done' && (
                  <>
                    <Prose text={first.outcome} />
                    <Resources metadata={first.metadata} agent={name} userId={userId} />
                  </>
                )}
                {first.status === 'failed' && <FailPanel turn={first} onAgain={() => runAgain(first.task)} statusHref={statusHref} />}
                {first.status === 'running' && run.turns.length === 1 && running && (
                  <div className="ag-working"><Icon name="spinner" spin /><span>{t('run.reportWorking')}</span> <button type="button" className="ag-link" onClick={() => pick('live')}>{t('run.watchLive')}</button></div>
                )}
                {first.status !== 'done' && first.status !== 'failed' && !(running && run.turns.length === 1) && (
                  <>
                    <p className="ag-sec__text">{t('run.noAnswer')}</p>
                    <div className="ag-sec__acts">
                      <button type="button" className="btn btn-primary btn-sm" onClick={() => runAgain(first.task)}><Icon name="refresh" /> {t('run.runAgain')}</button>
                    </div>
                  </>
                )}
              </section>

              {followUps.length > 0 && (
                <section className="ag-sec" aria-labelledby="ag-s-fu" data-testid="run-followups">
                  <div className="ag-sec__head"><h2 id="ag-s-fu">{t('run.followUps')}</h2></div>
                  {followUps.map((tn, i) => (
                    <div key={i} className="ag-followup">
                      <div className="ag-followup__q">{tn.task}</div>
                      {tn.status === 'done' && <Prose text={tn.outcome} />}
                      {tn.status === 'failed' && <FailPanel turn={tn} compact />}
                      {tn.status === 'running' && (
                        <div className="ag-working"><Icon name="spinner" spin /><span>{t('run.reportWorking')}</span> <button type="button" className="ag-link" onClick={() => pick('live')}>{t('run.watchLive')}</button></div>
                      )}
                    </div>
                  ))}
                </section>
              )}

              {evidence.length > 0 && <Evidence items={evidence} />}

              <section className="ag-sec" aria-labelledby="ag-s-steps">
                <div className="ag-sec__head"><h2 id="ag-s-steps">{t('run.steps')}</h2></div>
                <StepsList run={run} />
              </section>
            </div></article>
          </div>
        )}

        {dock}
      </div>
    </div>
  )
}

function liveLine(turn, t) {
  const tools = turn.steps.filter(s => s.kind === 'tool')
  const open = tools.findLast(s => s.result === undefined)
  if (open) return t('run.using', { name: open.name })
  if (turn.live.reasoning) return t('run.thinking')
  return t('run.working')
}

// eslint-disable-next-line no-unused-vars
function Evidence({ items }) {
  const { t } = useTranslation('agents')
  const [open, setOpen] = useState({})
  return (
    <section className="ag-sec" aria-labelledby="ag-s-ev" data-testid="run-evidence">
      <div className="ag-sec__head"><h2 id="ag-s-ev">{t('run.evidence')}</h2></div>
      <p className="ag-note">{t('run.evidenceNote')}</p>
      <ul className="ag-ev">
        {items.map((s, i) => (
          <li key={i} className="ag-ev__item" data-open={open[i] || undefined}>
            <div className="ag-ev__head"><Icon name="wrench" /><code>{s.name}</code></div>
            {s.args && <div className="ag-ev__args">{firstLine(s.args, 160)}</div>}
            <pre className="ag-ev__excerpt">{s.result}</pre>
            {s.result.length > 180 && (
              <button type="button" className="ag-ev__more" aria-expanded={!!open[i]} onClick={() => setOpen(o => ({ ...o, [i]: !o[i] }))}>
                {open[i] ? t('run.showLess') : t('run.showMore')}
              </button>
            )}
          </li>
        ))}
      </ul>
    </section>
  )
}

// eslint-disable-next-line no-unused-vars
function StepsList({ run }) {
  const { t } = useTranslation('agents')
  const multi = run.turns.length > 1
  const total = run.turns.reduce((n, tn) => n + tn.steps.length, 0)
  if (total === 0) return <p className="ag-note">{t('run.noStepsReport')}</p>
  let n = 0
  return (
    <ol className="ag-steps">
      {run.turns.flatMap((tn, ti) => {
        if (tn.steps.length === 0) return []
        const rows = tn.steps.map((s, si) => {
          n += 1
          return (
            <li key={`${ti}-${si}`}>
              <span className="ag-steps__n">{n}</span>
              <span className="ag-steps__what">
                {stepLabel(s, t)}
                {stepNote(s) && <small>{stepNote(s)}</small>}
              </span>
              {s.ts && tn.startedAt && !run.legacy && <span className="ag-time">{`+${formatDuration(Math.max(0, s.ts - tn.startedAt))}`}</span>}
            </li>
          )
        })
        return multi
          ? [<li key={`h${ti}`} className="ag-steps__turn">{ti === 0 ? t('run.task') : t('run.followUpN', { n: ti })}</li>, ...rows]
          : rows
      })}
    </ol>
  )
}
