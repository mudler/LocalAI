/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { BulkActions, SwitchGroup, SwitchHead, SwitchPopover, SwitchRow, SwitchStatus } from './SwitchList'
import { bulkIds, bulkable, rowOn, switchCounts } from '../../utils/chatSwitchRows'

// The Tools chip of the composer: one switch per tool source of this chat, in
// groups (built-in tools, add-ons, MCP servers of the model, your
// connectors), with "Select all" and "Disable all" for the whole menu and for
// each group of two or more. A row the chat cannot use now is shown off, with
// the reason, and no bulk action changes it. An opt-in row (saved agents) is
// switched on by hand only. The control renders nothing when there is nothing
// to switch and no notice.
//
// groups: [{ key, title, icon, onSet(ids, on), rows: [{ key, id, label, title,
//   on, usable, reason, note, warn, chip, meta, optIn, onToggle }] }]
// note is a line under the name (warn draws it as a warning); chip is a short
// label next to it (who serves the tool); meta is a muted text on the right.
// notice is a block over the groups, for something that holds for the whole
// chat (the chat cannot run commands, its network); the menu shows for it even
// with no rows.
export default function SwitchMenu({ icon, label, title, heading, note, notice, groups, error, footer, testId }) {
  const { t } = useTranslation('chat')
  const noteId = useId()
  const [busy, setBusy] = useState(false)

  const shown = groups.filter((g) => g.rows.length > 0)
  const rows = shown.flatMap((g) => g.rows)
  if (rows.length === 0 && !notice) return null
  const { on, total } = switchCounts(rows)
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
    <SwitchPopover icon={icon} label={label} title={title} count={on} describedBy={note ? noteId : undefined} testId={testId} menuClassName="swl-menu--wide">
      <SwitchHead
        title={heading}
        summary={rows.length > 0 ? t('tools.count', { on, total }) : null}
        summaryTestId={`${testId}-summary`}
        actions={bulkable(rows) > 1 ? actionsFor(shown, false) : null}
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
    </SwitchPopover>
  )
}

function ToolRow({ row: r, testId }) {
  const { t } = useTranslation('chat')
  const usable = r.usable !== false
  const reason = !usable ? (r.reason || t('tools.unavailable')) : ''
  const desc = r.note || (r.optIn && usable ? t('tools.optIn') : '')
  return (
    <SwitchRow
      name={r.label}
      desc={desc}
      tone={r.warn ? 'warn' : undefined}
      chip={r.chip}
      chipTestId={`${testId}-note`}
      meta={r.meta}
      reason={reason}
      title={r.title}
      checked={rowOn(r)}
      disabled={!usable}
      onChange={(next) => r.onToggle(next)}
      stacked={!!(reason || desc)}
      testId={`${testId}-item`}
      descTestId={r.note ? `${testId}-note` : undefined}
      data={{ optin: r.optIn ? 'true' : undefined }}
    />
  )
}
