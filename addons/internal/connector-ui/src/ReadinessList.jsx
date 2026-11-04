/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The readiness list at the top of the form: one row per thing that must agree
// for the bot to work, each with a state, one sentence and at most one fix.
// The rows come from access.js; this file only draws them.
import { useState } from 'react'
import Icon from '../../host-compat/src/components/Icon'
import { FixButton } from './CapabilitiesPanel'
import { readinessRows, visibleRows } from './access'

export const READY_TEXT = {
  'ready.title': 'Readiness',
  'ready.help': 'What has to be in place for this bot to work, for the form as it is now.',
  'ready.checkingSummary': 'Checking what the form will do.',
  'ready.allGood': 'Everything needed is in place.',
  'ready.problems': '{{count}} to look at.',
  'ready.showAll': 'Show all {{count}}',
  'ready.showProblems': 'Show only what needs attention',
  'ready.checking': 'Checking.',
  'ready.unchecked': 'Could not check this. See "What this bot can do" below.',
  'ready.row.token': 'Token',
  'ready.row.agent': 'Agent',
  'ready.row.who': 'Who may use it',
  'ready.row.permissions': 'Permissions',
  'ready.row.triggers': 'Triggers',
  'ready.row.files': 'Files',
  'ready.row.notify': 'Notifications',
  'ready.state.ok': 'OK',
  'ready.state.attention': 'Needs attention',
  'ready.state.off': 'Off',
  'ready.state.after_save': 'After you save',
  'ready.state.broken': 'Not working',
  'ready.state.checking': 'Checking',
  'ready.state.unknown': 'Unknown',
  'ready.token.missing': 'Paste the token of the bot. Without it nothing is received.',
  'ready.token.newTyped': 'The token is checked with the platform when you save.',
  'ready.token.typed': 'The new token is checked with the platform when you save.',
  'ready.token.failed': 'The platform refused the token: {{reason}}',
  'ready.token.checked': 'The platform accepted the token. {{bot}}',
  'ready.token.saved': 'A token is saved. The platform accepted it when it was saved.',
  'ready.agent.none': 'Choose the agent that answers.',
  'ready.agent.credentialsOnly': 'No agent. This connection only holds the credentials and answers no message.',
  'ready.agent.gone': 'The chosen agent is not in your list any more. Choose another.',
  'ready.agent.noModel': '{{name}} has no model, so it cannot answer. Give it one.',
  'ready.agent.ok': '{{name}} answers, with the model {{model}}.',
  'ready.agent.okUnknown': 'An agent is chosen.',
  'ready.who.anyone': 'Anyone who can message the bot may use it.',
  'ready.who.nobody': 'Nobody is allowed yet, so the bot ignores every message. Add a person.',
  'ready.who.channelsOnly': 'Only channels are on the list. People in a channel can chat, but they cannot use any permission until you add them as people.',
  'ready.who.listed_one': '{{count}} person or chat is allowed.',
  'ready.who.listed_other': '{{count}} people or chats are allowed.',
  'ready.who.mixed': '{{count}} entries are allowed, people and channels.',
  'ready.perm.none': 'No permission is on. Senders can chat, and nothing else.',
  'ready.perm.noneSaved': 'No permission is on, so files, memory, triggers and posts are off.',
  'ready.perm.onlyOwnRows': 'The other permissions are off. Triggers and files are below.',
  'ready.perm.ok_one': '{{count}} permission is on and a sender can use it.',
  'ready.perm.ok_other': '{{count}} permissions are on and a sender can use them.',
  'ready.perm.dead_one': 'Nobody can use {{names}}. {{message}}',
  'ready.perm.dead_other': 'Nobody can use {{count}} of the permissions that are on: {{names}}. First: {{message}}',
  'ready.triggers.off': 'The agent cannot set reminders or scheduled tasks here.',
  'ready.triggers.blocked': 'Triggers are on, and they cannot be used. {{message}}',
  'ready.triggers.ok.ask': 'The agent can propose triggers. You confirm each one before it starts.',
  'ready.triggers.ok.auto': 'The agent can propose triggers. They start at once, without asking you.',
  'ready.triggers.ok.auto_readonly': 'The agent can propose triggers. A trigger that only reads starts at once; you confirm any other.',
  'ready.files.offReceive': 'Senders cannot send files to the agent.',
  'ready.files.offBoth': 'Senders cannot send files to the agent, and the bot sends none back.',
  'ready.files.blocked': 'Receiving files is on, and it cannot be used. {{message}}',
  'ready.files.ok': 'Senders can send files to the agent, and the bot can send files back.',
  'ready.files.okReceive': 'Senders can send files to the agent. The bot sends none back.',
  'ready.notify.off': 'No chat gets notices from this bot.',
  'ready.notify.ok': 'Notices go to the chat {{chat}}.',
}

// The words of a row. A key with a count has a _one and a _other form.
function sentence(row, s) {
  const params = row.params || {}
  const plural = row.key === 'ready.who.listed' || row.key === 'ready.perm.ok' || row.key === 'ready.perm.dead'
  const key = plural ? `${row.key}_${params.count === 1 ? 'one' : 'other'}` : row.key
  return s(key, params)
}

const ICON = { ok: 'check-circle', attention: 'warning', off: 'minus', after_save: 'save', broken: 'alert-circle', checking: 'spinner', unknown: 'help-circle' }

export default function ReadinessList({ rowsInput, s, ctx, onTest, onSave }) {
  const rows = readinessRows(rowsInput)
  const [expanded, setExpanded] = useState(false)
  const { shown, hidden } = visibleRows(rows, expanded)
  const problems = rows.length - rows.filter((r) => ['ok', 'off', 'checking'].includes(r.state)).length
  return (
    <section className="cn-ready" aria-labelledby="cn-ready-title" data-testid="connector-readiness">
      <div className="cn-ready__head">
        <h2 id="cn-ready-title" className="cn-sub__title">{s('ready.title')}</h2>
        <span className="cn-muted" data-testid="connector-ready-summary">{problems > 0 ? s('ready.problems', { count: problems }) : rows.some((r) => r.state === 'checking') ? s('ready.checkingSummary') : s('ready.allGood')}</span>
      </div>
      <ul className="cn-ready__list" aria-label={s('ready.title')}>
        {shown.map((r) => (
          <li key={r.id} className="cn-ready__row" data-testid={`connector-ready-${r.id}`} data-state={r.state}>
            <span className={`cn-ready__state cn-ready__state--${r.state}`}>
              <Icon name={ICON[r.state] || 'info'} />
              <span>{s(`ready.state.${r.state}`)}</span>
            </span>
            <div className="cn-ready__text">
              <span className="cn-ready__label">{s(`ready.row.${r.id}`)}</span>
              <span className="cn-ready__sentence">{sentence(r, s)}</span>
            </div>
            {r.fix && <div className="cn-ready__fix"><FixButton fix={r.fix} ctx={ctx} s={s} onTest={onTest} onSave={onSave} /></div>}
          </li>
        ))}
      </ul>
      {(hidden > 0 || expanded) && (
        <button type="button" className="cn-hint__more cn-ready__toggle" aria-expanded={expanded} data-testid="connector-ready-toggle" onClick={() => setExpanded(!expanded)}>
          {expanded ? s('ready.showProblems') : s('ready.showAll', { count: rows.length })}
        </button>
      )}
    </section>
  )
}
