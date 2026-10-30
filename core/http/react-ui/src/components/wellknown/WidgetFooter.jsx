/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { chatTriggersApi } from '../../utils/api'
import { formatNext } from '../../utils/chatTrigger'
import { plainSummary } from '../../utils/widgetTrigger'

function shortTime(ts, locale) {
  if (!ts) return ''
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ''
  try {
    return new Date().toDateString() === d.toDateString()
      ? d.toLocaleTimeString(locale, { hour: 'numeric', minute: '2-digit' })
      : d.toLocaleDateString(locale, { weekday: 'long' })
  } catch { return '' }
}

// dayOf names the day of a time: today, or the weekday.
function dayOf(ts, locale) {
  if (!ts) return ''
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ''
  if (new Date().toDateString() === d.toDateString()) return null
  try { return d.toLocaleDateString(locale, { weekday: 'long' }) } catch { return '' }
}

const fmt = (n, locale) => {
  try { return new Intl.NumberFormat(locale).format(n) } catch { return String(n) }
}

// The one line under a widget that says where its data comes from and, for a
// widget a trigger feeds, when it runs and what a person can do about it. The
// line has words and an icon for every state, never colour alone; id lets the
// widget point at it with aria-describedby.
//
//   state     from footerState
//   w         the widget of home.json
//   d         the widget's data (widgetData)
//   trigger   the linked trigger, or null; proposal the waiting proposal
//   act       runs a trigger action and reads the triggers again
//   onRevert  writes the last good version of the data file
export default function WidgetFooter({ id, state, w, d, trigger, proposal, act, onRevert }) {
  const { t, i18n } = useTranslation('workspace')
  const navigate = useNavigate()
  const k = (key, o) => t(`wellKnown.home.footer.${key}`, o)
  const lang = i18n.language
  const schedule = plainSummary(trigger)
  const updated = shortTime(d.updatedAt || trigger?.last_done, lang)
  const run = (call) => act(() => call(trigger.id))
  const btn = (label, onClick, testid) => <button type="button" className="wk-foot__act" onClick={onClick} data-testid={testid}>{label}</button>
  const details = trigger && <Link className="wk-foot__act" to={`/app/triggers/${encodeURIComponent(trigger.id)}`} data-testid="widget-details">{k('details')}</Link>
  const fix = () => navigate('/app/chats', { state: { draft: k('fixPrompt', { file: w.source }) } })

  let tone = ''
  let icon = 'clock'
  let text
  let acts = null
  switch (state) {
    case 'invalid':
      tone = 'err'; icon = 'alert-circle'
      text = k('invalid', { file: w.source })
      acts = onRevert && btn(k('revert'), onRevert, 'widget-revert')
      break
    case 'missing':
      icon = 'file'
      text = k('missing', { file: w.source })
      acts = btn(k('fix'), fix, 'widget-fix')
      break
    case 'waiting':
      icon = 'hourglass'
      text = k('waiting')
      acts = proposal?.source_chat_id && <Link className="wk-foot__act" to={`/app/chats/${encodeURIComponent(proposal.source_chat_id)}`} data-testid="widget-open-chat">{k('openChat')}</Link>
      break
    case 'running':
      icon = 'spinner'
      text = k('running')
      acts = trigger.last_run?.chat_id
        ? <Link className="wk-foot__act" to={`/app/chats/${encodeURIComponent(trigger.last_run.chat_id)}`} data-testid="widget-open-run">{k('openRun')}</Link>
        : details
      break
    case 'paused':
      icon = 'pause'
      text = k('paused', { schedule })
      acts = btn(k('resume'), () => run(chatTriggersApi.enable), 'widget-resume')
      break
    case 'failedEmpty':
      tone = 'err'; icon = 'alert-circle'
      text = k('failedEmpty')
      acts = <>{details}{btn(k('retry'), () => run(chatTriggersApi.run), 'widget-retry')}</>
      break
    case 'stale':
      tone = 'warn'; icon = 'warning'
      {
        const day = dayOf(d.updatedAt || trigger?.last_done, lang)
        text = day === null ? k('staleToday') : k('stale', { when: day || k('earlier') })
      }
      acts = <>{details}{btn(k('retry'), () => run(chatTriggersApi.run), 'widget-retry')}</>
      break
    case 'firstRun':
      text = trigger.next_run ? k('firstRun', { when: formatNext(trigger.next_run, trigger.time_zone) }) : k('firstRunFile')
      acts = btn(k('runNow'), () => run(chatTriggersApi.run), 'widget-run-now')
      break
    case 'fresh':
      text = updated ? k('fresh', { schedule, time: updated }) : schedule
      acts = btn(k('pause'), () => run(chatTriggersApi.disable), 'widget-pause')
      break
    case 'page':
      icon = 'file-code'
      text = k('fromPage', { title: d.source?.page || '' })
      break
    default:
      icon = 'file'
      text = k('fromFile', { file: w.source })
  }

  // The next run and what a run costs, under the line, for a trigger that is
  // on: the numbers that do not change between runs stay out of the main line.
  const cost = trigger?.cost
  const tokens = cost?.average_tokens || cost?.estimated_tokens || 0
  const extra = []
  if (trigger?.enabled && trigger.next_run && state !== 'firstRun') extra.push(k('next', { when: formatNext(trigger.next_run, trigger.time_zone) }))
  if (trigger && tokens > 0) extra.push(k(cost.remote ? 'costRemote' : 'cost', { tokens: fmt(tokens, lang) }))

  return (
    <div id={id} className={`wk-foot${tone ? ` wk-foot--${tone}` : ''}`} data-testid="widget-footer" data-state={state} role={tone ? 'status' : undefined}>
      <p className="wk-foot__line">
        <Icon name={icon} spin={state === 'running'} />
        <span className="wk-foot__text">{text}</span>
        {acts && <span className="wk-foot__acts">{acts}</span>}
      </p>
      {extra.length > 0 && <p className="wk-foot__extra">{extra.join(' · ')}</p>}
    </div>
  )
}
