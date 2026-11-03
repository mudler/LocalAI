/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import { BulkActions, SwitchGroup, SwitchHead, SwitchPopover, SwitchRow, SwitchStatus } from './SwitchList'
import { bulkIds, bulkable, rowOn, switchCounts, switchRows } from '../../utils/chatSwitchRows'

// The Tools chip of the composer: one switch per tool source of this chat, in
// groups (built-in tools, add-ons, MCP servers of the model, your
// connectors), with "Select all" and "Disable all" for the whole menu and for
// each group of two or more. A row the chat cannot use now is shown off, with
// the reason, and no bulk action changes it. An opt-in row (saved agents) is
// switched on by hand only. The control renders nothing when there is nothing
// to switch and no notice.
//
// groups: [{ key, title, icon, onSet(ids, on), rows: [{ key, id, label, title,
//   on, usable, reason, description, whenOff, note, warn, chip, meta, optIn,
//   onToggle }] }]
// description says what the model can do with the switch on, whenOff what it
// loses once the switch is off; both show under the name of every row. note is a line under the name (warn draws it as a warning); chip is a short
// label next to it (who serves the tool); meta is a muted text on the right.
// notice is a block over the groups, for something that holds for the whole
// chat (the chat cannot run commands, its network); the menu shows for it even
// with no rows.
export default function SwitchMenu({ icon, label, title, heading, note, notice, groups, error, footer, testId }) {
  const noteId = useId()
  const rows = groups.filter((g) => g.rows.length > 0).flatMap((g) => g.rows)
  if (rows.length === 0 && !notice) return null
  const { on } = switchCounts(switchRows(groups))
  return (
    <SwitchPopover icon={icon} label={label} title={title} count={on} describedBy={note ? noteId : undefined} testId={testId} menuClassName="swl-menu--wide">
      <SwitchPanel heading={heading} note={note} noteId={noteId} notice={notice} groups={groups} error={error} footer={footer} testId={testId} />
    </SwitchPopover>
  )
}

// The body of the Tools list, without the chip: the header with the count and
// the bulk actions, the groups with their rows, and the status line. The
// Tools menu of a chat puts it in a popover; the agent form draws it inline,
// so both show the same switches the same way.
export function SwitchPanel({ heading, note, noteId, notice, groups, error, footer, testId }) {
  const { t } = useTranslation('chat')
  const [busy, setBusy] = useState(false)

  const shown = groups.filter((g) => g.rows.length > 0)
  const rows = shown.flatMap((g) => g.rows)
  // The count and the bulk actions cover the groups that have a setter. The
  // grants group has none: its rows are rights, not tools, and Select all
  // must never grant one.
  const counted = switchRows(shown)
  const { on, total } = switchCounts(counted)
  const titled = shown.length > 1

  // One request per setter, one after the other: two groups can share one
  // (the MCP servers and the connectors), and their ids go together.
  const setGroups = async (list, value) => {
    const bySetter = new Map()
    for (const g of list) {
      const ids = bulkIds(g.rows, value)
      if (g.onSet && ids.length > 0) bySetter.set(g.onSet, [...(bySetter.get(g.onSet) || []), ...ids])
    }
    setBusy(true)
    try {
      for (const [onSet, ids] of bySetter) await onSet(ids, value)
    } finally {
      setBusy(false)
    }
  }
  const actionsFor = (list, compact, name) => (
    <BulkActions
      compact={compact}
      testId={compact ? `${testId}-group` : testId}
      allLabel={t(compact ? 'tools.groupOn' : 'tools.selectAll')}
      noneLabel={t(compact ? 'tools.groupOff' : 'tools.disableAll')}
      allAria={compact ? t('tools.groupOnLabel', { group: name }) : undefined}
      noneAria={compact ? t('tools.groupOffLabel', { group: name }) : undefined}
      allDisabled={busy || list.every((g) => bulkIds(g.rows, true).length === 0)}
      noneDisabled={busy || list.every((g) => bulkIds(g.rows, false).length === 0)}
      onAll={() => setGroups(list, true)}
      onNone={() => setGroups(list, false)}
    />
  )

  return (
    <>
      <SwitchHead
        title={heading}
        summary={counted.length > 0 ? t('tools.count', { on, total }) : null}
        summaryTestId={`${testId}-summary`}
        actions={bulkable(counted) > 1 ? actionsFor(shown.filter((g) => g.onSet), false) : null}
      >
        {note && <p className="swl-head__note" id={noteId}>{note}</p>}
      </SwitchHead>
      <div className="swl-list" data-testid={`${testId}-list`}>
        {notice && <div className="swl-notice">{notice}</div>}
        {rows.length === 0 && <p className="swl-empty" data-testid={`${testId}-empty`}>{t('tools.empty')}</p>}
        {shown.map((g) => {
          const c = switchCounts(g.rows)
          return (
            <SwitchGroup
              key={g.key}
              title={g.title}
              icon={g.icon}
              count={titled ? t('tools.groupCount', { on: c.on, total: c.total }) : null}
              actions={titled && g.onSet && bulkable(g.rows) > 1 ? actionsFor([g], true, g.title) : null}
              testId={`${testId}-group-${g.key}`}
            >
              {g.rows.map((r) => <ToolRow key={r.key} row={r} testId={testId} />)}
            </SwitchGroup>
          )
        })}
      </div>
      <SwitchStatus error={error} failed={t('tools.failed')} testId={`${testId}-error`}>{footer}</SwitchStatus>
    </>
  )
}

function ToolRow({ row: r, testId }) {
  const { t } = useTranslation('chat')
  const usable = r.usable !== false
  const reason = !usable ? (r.reason || t('tools.unavailable')) : ''
  const desc = r.note || (r.optIn && usable ? t('tools.optIn') : '')
  const on = rowOn(r)
  // What the switch does, and, once it is off, what the model loses.
  const blurb = [r.description, !on && usable ? r.whenOff : ''].filter(Boolean).join(' ')
  return (
    <SwitchRow
      name={r.label}
      blurb={blurb}
      blurbTestId={blurb ? `${testId}-blurb` : undefined}
      desc={desc}
      tone={r.warn ? 'warn' : undefined}
      chip={r.chip}
      chipTestId={`${testId}-note`}
      meta={r.meta}
      reason={reason}
      title={r.title}
      checked={rowOn(r)}
      disabled={!usable || !!r.readOnly}
      onChange={(next) => r.onToggle(next)}
      stacked={!!(reason || desc)}
      testId={`${testId}-item`}
      descTestId={r.note ? `${testId}-note` : undefined}
      data={{ optin: r.optIn ? 'true' : undefined }}
      extra={r.pick ? <RowPick pick={r.pick} testId={testId} /> : undefined}
    />
  )
}

// A choice under a row, with its name, a description of what it does and,
// when there is nothing to choose from, a link to where to make one. The Tools
// menu of a chat and the agent form draw it the same way.
//
// pick: { label, hint, value, options: [{ value, label }], placeholder,
//   clearLabel (an empty choice that stays in the list), disabled, onChange(value), link: { to, label } }
function RowPick({ pick: p, testId }) {
  const id = useId()
  const hintId = useId()
  return (
    <div className="swl-pick" data-testid={`${testId}-pick`}>
      <label className="swl-pick__label" htmlFor={id}>{p.label}</label>
      {p.options.length > 0 && (
        <select
          id={id}
          className="dk-select swl-pick__select"
          value={p.value || ''}
          disabled={p.disabled}
          aria-describedby={p.hint ? hintId : undefined}
          data-testid={`${testId}-pick-select`}
          onChange={(e) => p.onChange(e.target.value)}
        >
          {(!p.value || p.clearLabel) && <option value="">{p.clearLabel || p.placeholder}</option>}
          {p.options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
        </select>
      )}
      {p.hint && <span className="swl-pick__hint" id={hintId} data-testid={`${testId}-pick-hint`}>{p.hint}</span>}
      {p.link && <Link className="swl-pick__link" to={p.link.to} data-testid={`${testId}-pick-link`}>{p.link.label}</Link>}
    </div>
  )
}
