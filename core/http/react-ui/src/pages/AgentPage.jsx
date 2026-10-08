import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
// eslint-disable-next-line no-unused-vars
import { Link, useLocation, useNavigate, useOutletContext, useParams, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { agentsApi } from '../utils/api'
import { agentInfo, workingLine } from '../utils/agentInfo'
import { clearRuns, effectiveStatus, loadRuns, newRunId, outcomeLine, recordStatus, firstLine, titleOf, STRIP_LENGTH } from '../utils/agentRuns'
// eslint-disable-next-line no-unused-vars
import { StatusMark, RunStrip, RecordLine, Chip, LibraryChips, formatWhen, agentPath } from '../components/agents/AgentBits'
// eslint-disable-next-line no-unused-vars
import ActionMenu from '../components/ActionMenu'
// eslint-disable-next-line no-unused-vars
import ConfirmDialog from '../components/ConfirmDialog'
import Icon from '../components/Icon'
import './agents.css'

// An agent as a home: what it is, its record, a box to give it a task, and the
// runs it has done. The facts come from its saved config; the record is the
// log this browser keeps (see utils/agentRuns.js).
export default function AgentPage() {
  const { name } = useParams()
  const { t } = useTranslation('agents')
  const { addToast } = useOutletContext()
  const navigate = useNavigate()
  const location = useLocation()
  const [searchParams] = useSearchParams()
  const userId = searchParams.get('user_id') || undefined

  const [config, setConfig] = useState(null)
  const [active, setActive] = useState(null)
  const [missing, setMissing] = useState(false)
  const [loading, setLoading] = useState(true)
  const [working, setWorking] = useState(null)
  const [tick, setTick] = useState(0)
  const [showAll, setShowAll] = useState(false)
  const [task, setTask] = useState(() => location.state?.task || '')
  const [confirm, setConfirm] = useState(null)
  const taRef = useRef(null)

  const load = useCallback(async () => {
    try {
      const [state, cfg] = await Promise.all([
        agentsApi.get(name, userId),
        agentsApi.getConfig(name, userId).catch(() => null),
      ])
      setActive(state?.active !== false)
      setConfig(cfg)
      setMissing(false)
    } catch (err) {
      if (err?.status === 404 || /not found/i.test(err?.message || '')) setMissing(true)
      else addToast(t('toasts.loadFailed', { message: err.message }), 'error')
    } finally {
      setLoading(false)
    }
  }, [name, userId, addToast, t])

  useEffect(() => { load() }, [load])

  // Work in flight shows as "Working" on the page that opens the agent.
  useEffect(() => {
    let stop = false
    const poll = async () => {
      try {
        const obs = await agentsApi.observables(name, userId)
        if (!stop) setWorking(workingLine(obs?.History || []))
      } catch (_e) { /* the badge simply stays off */ }
      if (!stop) setTick(n => n + 1)
    }
    poll()
    const id = setInterval(poll, 5000)
    return () => { stop = true; clearInterval(id) }
  }, [name, userId])

  const info = useMemo(() => agentInfo(config), [config])
  // tick re-reads the log while the page is open
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const runs = useMemo(() => loadRuns(name, userId), [name, userId, tick])
  const liveRun = runs.find(r => effectiveStatus(r) === 'running')
  const state = working || liveRun ? 'running' : active === false ? 'paused' : 'ready'
  const paused = active === false

  const start = () => {
    const text = task.trim()
    if (!text || paused) return
    const id = newRunId()
    navigate(agentPath(name, userId, `/runs/${id}`), { state: { task: text } })
  }

  const onKeyDown = (e) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.ctrlKey && !e.metaKey && !e.altKey && !e.nativeEvent?.isComposing && e.keyCode !== 229) {
      e.preventDefault()
      start()
    }
  }

  const pauseResume = async () => {
    try {
      if (paused) {
        await agentsApi.resume(name, userId)
        addToast(t('toasts.resumed', { name }), 'success')
      } else {
        await agentsApi.pause(name, userId)
        addToast(t('toasts.paused', { name }), 'success')
      }
      load()
    } catch (err) {
      addToast(t(paused ? 'toasts.resumeFailed' : 'toasts.pauseFailed', { message: err.message }), 'error')
    }
  }

  const exportAgent = async () => {
    try {
      const data = await agentsApi.export(name, userId)
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `${name}.json`
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
      URL.revokeObjectURL(url)
      addToast(t('toasts.exported', { name }), 'success')
    } catch (err) {
      addToast(t('toasts.exportFailed', { message: err.message }), 'error')
    }
  }

  const askClear = () => setConfirm({
    title: t('agent.clearTitle'),
    message: t('agent.clearMessage', { name }),
    confirmLabel: t('agent.clearConfirm'),
    danger: true,
    onConfirm: () => {
      setConfirm(null)
      clearRuns(name, userId)
      setTick(n => n + 1)
      addToast(t('agent.cleared'), 'success')
    },
  })

  const askDelete = () => setConfirm({
    title: t('deleteDialog.title'),
    message: t('deleteDialog.message', { name }),
    confirmLabel: t('deleteDialog.confirm'),
    danger: true,
    onConfirm: async () => {
      setConfirm(null)
      try {
        await agentsApi.delete(name, userId)
        addToast(t('toasts.deleted', { name }), 'success')
        navigate('/app/agents')
      } catch (err) {
        addToast(t('toasts.deleteFailed', { message: err.message }), 'error')
      }
    },
  })

  if (loading) {
    return <div className="page page--medium loading-center"><Icon name="spinner" spin className="icon-xl text-primary" /></div>
  }

  if (missing) {
    return (
      <div className="page page--medium ag-page">
        <div className="ag-home">
          <Link className="ag-back" to="/app/agents"><Icon name="arrow-left" /> {t('agent.back')}</Link>
          <div className="ag-empty" data-testid="agent-missing">
            <h1 className="ag-title">{t('agent.missingTitle', { name })}</h1>
            <p>{t('agent.missingText')}</p>
            <Link className="btn btn-primary" to="/app/agents">{t('agent.back')}</Link>
          </div>
        </div>
      </div>
    )
  }

  const shown = showAll ? runs : runs.slice(0, STRIP_LENGTH)
  const hasTools = info.tools.length + info.mcp.length > 0

  return (
    <div className="page page--medium ag-page" data-testid="agent-page">
      <div className="ag-home">
        <div className="ag-bar">
          <Link className="ag-back" to="/app/agents"><Icon name="arrow-left" /> {t('agent.back')}</Link>
          <div className="ag-bar__acts">
            <Link className="btn btn-secondary btn-sm" to={agentPath(name, userId, '/status')}>
              <Icon name="chart-bar" /> {t('agent.status')}
            </Link>
            <Link className="btn btn-secondary btn-sm" to={agentPath(name, userId, '/edit')}>
              <Icon name="edit" /> {t('actions.edit')}
            </Link>
            <ActionMenu
              ariaLabel={t('actions.moreFor', { name })}
              items={[
                { key: 'pause', icon: paused ? 'play' : 'pause', label: paused ? t('actions.resume') : t('actions.pause'), onClick: pauseResume },
                { key: 'export', icon: 'download', label: t('actions.export'), onClick: exportAgent },
                { key: 'clear', icon: 'eraser', label: t('agent.clearRecord'), onClick: askClear, disabled: runs.length === 0 },
                { divider: true },
                { key: 'delete', icon: 'trash', label: t('actions.delete'), danger: true, onClick: askDelete },
              ]}
            />
          </div>
        </div>

        <header>
          <div className="ag-title">
            <h1>{name}</h1>
            <StatusMark status={state} />
          </div>
          {info.description && <p className="ag-lede">{info.description}</p>}
        </header>

        <dl className="ag-facts" data-testid="agent-facts">
          <dt>{t('facts.model')}</dt>
          <dd className="ag-facts__model">{info.model || <span className="ag-muted">{t('facts.noModel')}</span>}</dd>
          <dt>{t('facts.tools')}</dt>
          <dd>
            {hasTools
              ? <>
                {info.tools.map(x => <Chip key={x} mono>{x}</Chip>)}
                {info.mcp.map(x => <Chip key={`mcp-${x}`} mono>{`mcp: ${x}`}</Chip>)}
              </>
              : <span className="ag-muted">{t('chips.none')}</span>}
          </dd>
          <dt>{t('facts.memory')}</dt>
          <dd><LibraryChips info={info} kind="memory" /></dd>
          <dt>{t('facts.skills')}</dt>
          <dd><LibraryChips info={info} kind="skills" /></dd>
          {info.instructions && (
            <>
              <dt>{t('facts.instructions')}</dt>
              <dd className="ag-facts__text">{firstLine(info.instructions, 220)}</dd>
            </>
          )}
          {info.schedule && (
            <>
              <dt>{t('facts.schedule')}</dt>
              <dd className="ag-facts__model">{info.schedule}</dd>
            </>
          )}
          <dt>{t('facts.record')}</dt>
          <dd><span className="ag-record"><RunStrip runs={runs} /><RecordLine runs={runs} /></span></dd>
        </dl>

        <section className="ag-task" aria-label={t('agent.taskLabel')}>
          <div className="dk-composer" aria-disabled={paused || undefined} data-testid="agent-task">
            <label className="dk-sr-only" htmlFor="ag-task-input">{t('agent.taskLabel')}</label>
            <textarea
              id="ag-task-input"
              ref={taRef}
              className="dk-composer-input"
              rows={2}
              value={task}
              disabled={paused}
              placeholder={t('agent.taskPlaceholder')}
              onChange={(e) => setTask(e.target.value)}
              onKeyDown={onKeyDown}
            />
            <div className="dk-composer-bar">
              <span className="dk-composer-hint">{t('agent.taskHint')}</span>
              <button type="button" className="dk-btn dk-btn--primary" onClick={start} disabled={!task.trim() || paused}>
                <Icon name="play" /> {t('agent.start')}
              </button>
            </div>
          </div>
          {paused && (
            <p className="ag-task__note">
              {t('agent.pausedNote')} <button type="button" className="ag-link" onClick={pauseResume}>{t('actions.resume')}</button>
            </p>
          )}
        </section>

        <section aria-labelledby="ag-runs-h">
          <div className="ag-section-head">
            <h2 className="ag-eyebrow" id="ag-runs-h">{t('agent.runs')}</h2>
            {runs.length > 0 && <span className="ag-muted ag-small">{t('agent.runsCount', { count: runs.length })}</span>}
          </div>
          {runs.length === 0 ? (
            <div className="ag-empty">
              <h3>{t('agent.noRunsTitle')}</h3>
              <p>{t('agent.noRunsText')}</p>
            </div>
          ) : (
            <div className="ag-runs" data-testid="agent-runs">
              {shown.map(r => {
                const st = recordStatus(r)
                const first = r.turns[0]
                return (
                  <Link key={r.id} className="ag-run-row" to={agentPath(name, userId, `/runs/${r.id}`)} data-run={r.id}>
                    <StatusMark status={st} />
                    <span className="ag-run-row__main">
                      <span className="ag-run-row__title">{titleOf(first.task, 90)}</span>
                      <span className="ag-run-row__line">{st === 'stopped' && !first.outcome ? t('run.stoppedLine') : st === 'running' ? t('run.workingLine') : outcomeLine(r)}</span>
                    </span>
                    <span className="ag-run-row__when">{formatWhen(r.startedAt)}</span>
                    <Icon name="chevron-right" />
                  </Link>
                )
              })}
            </div>
          )}
          {runs.length > STRIP_LENGTH && (
            <button type="button" className="ag-btn-quiet" onClick={() => setShowAll(v => !v)}>
              {showAll ? t('agent.showFewer') : t('agent.showOlder', { count: runs.length - STRIP_LENGTH })}
            </button>
          )}
          <p className="ag-note ag-note--gap">{t('record.where')}</p>
        </section>
      </div>

      <ConfirmDialog
        open={!!confirm}
        title={confirm?.title}
        message={confirm?.message}
        confirmLabel={confirm?.confirmLabel}
        danger={confirm?.danger}
        onConfirm={confirm?.onConfirm}
        onCancel={() => setConfirm(null)}
      />
    </div>
  )
}
