// eslint-disable-next-line no-unused-vars
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { recordStats, stripOf, STRIP_LENGTH } from '../../utils/agentRuns'
import Icon from '../Icon'

// A state in words with its dot. The dot never carries the meaning alone.
const STATUS_DOT = { running: 'accent', done: 'ok', failed: 'error', stopped: '', ready: '', paused: 'warn' }

export function StatusMark({ status, label }) {
  const { t } = useTranslation('agents')
  const dot = STATUS_DOT[status]
  return (
    <span className="ag-state" data-state={status}>
      <span className={`dk-dot${dot ? ` dk-dot--${dot}` : ''}`} aria-hidden="true" />
      {label || t(`state.${status}`)}
    </span>
  )
}

// The last 14 runs, oldest first: round finished, square failed, hollow
// stopped. Empty places keep the strip the same width for every agent.
export function RunStrip({ runs }) {
  const { t } = useTranslation('agents')
  const strip = stripOf(runs)
  const pad = Math.max(0, STRIP_LENGTH - strip.length)
  const describe = strip.length
    ? t('strip.label', { count: strip.length, done: strip.filter(s => s.status === 'done').length, failed: strip.filter(s => s.status === 'failed').length })
    : t('strip.none')
  return (
    <span className="ag-strip" role="img" aria-label={describe} data-testid="agent-run-strip">
      {Array.from({ length: pad }).map((_, i) => <i key={`e${i}`} className="ag-pip" data-state="empty" />)}
      {strip.map(s => <i key={s.id} className="ag-pip" data-state={s.status} />)}
    </span>
  )
}

// "93% of 41 runs finished, median 38 s", or the honest empty line.
export function RecordLine({ runs }) {
  const { t } = useTranslation('agents')
  if (runs.length === 0) return <span className="ag-muted">{t('record.none')}</span>
  const s = recordStats(runs)
  const pct = Math.round((s.finished / s.total) * 100)
  return (
    <span className="ag-muted">
      {t('record.line', { pct, count: s.total })}
    </span>
  )
}

export function Chip({ to, icon, children, mono = false, count, title }) {
  const cls = `ag-chip${mono ? ' ag-chip--mono' : ''}${to ? ' ag-chip--link' : ''}`
  const body = (
    <>
      {icon && <Icon name={icon} />}
      <span className="ag-chip__text">{children}</span>
      {count != null && <span className="ag-chip__count">{count}</span>}
    </>
  )
  return to
    ? <Link className={cls} to={to} title={title}>{body}</Link>
    : <span className={cls} title={title}>{body}</span>
}

// Memory and skills an agent has switched on, as chips that open the shared
// Library pages. An agent with none says so.
export function LibraryChips({ info, kind }) {
  const { t } = useTranslation('agents')
  if (kind === 'memory') {
    if (info.memory.length === 0) return <span className="ag-muted">{t('chips.none')}</span>
    return info.memory.map(m => (
      <Chip key={m} to="/app/collections" icon="database">{t(`chips.memory.${m === 'knowledge' ? 'knowledge' : 'longTerm'}`)}</Chip>
    ))
  }
  if (info.skills.length === 0) return <span className="ag-muted">{t('chips.none')}</span>
  return info.skills.map(s => (
    <Chip key={s} to="/app/skills" icon="sparkles">{s === 'all' ? t('chips.allSkills') : s}</Chip>
  ))
}

export function formatWhen(ts, now = Date.now()) {
  if (!ts) return ''
  const d = new Date(ts)
  const today = new Date(now)
  const time = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  if (d.toDateString() === today.toDateString()) return time
  const yesterday = new Date(now - 86400000)
  if (d.toDateString() === yesterday.toDateString()) return `${yesterdayWord()} ${time}`
  const sameYear = d.getFullYear() === today.getFullYear()
  return d.toLocaleDateString([], sameYear ? { month: 'short', day: 'numeric' } : { year: 'numeric', month: 'short', day: 'numeric' })
}

function yesterdayWord() {
  try {
    return new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' }).format(-1, 'day')
  } catch {
    return 'Yesterday'
  }
}

export function userQuery(userId) {
  return userId ? `?user_id=${encodeURIComponent(userId)}` : ''
}

export function agentPath(name, userId, tail = '') {
  return `/app/agents/${encodeURIComponent(name)}${tail}${userQuery(userId)}`
}
