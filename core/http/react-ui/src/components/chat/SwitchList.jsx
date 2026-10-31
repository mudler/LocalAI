/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { Button, StatusChip, Switch } from '../ui'
import './switchlist.css'

// The parts of the composer's switch lists, the Tools menu and the Skills
// picker: the chip that opens the list, a fixed header (title, "3 of 5 on",
// Select all and Disable all), groups with their own count and actions, one
// row per switch, and a status line at the bottom that always takes its room,
// so a refused change never moves the rows. On a phone the list is a sheet over
// the bottom of the screen.

// The chip and its menu. The menu closes on a click outside and on Escape,
// which gives the focus back to the chip.
//
//   icon, label, title   the chip
//   count                the number on the chip (nothing at 0)
//   describedBy          the id of a note of the open menu, for the chip
//   children(close)      the menu content
export function SwitchPopover({ icon, label, title, count, describedBy, testId, menuClassName = '', children }) {
  const [open, setOpen] = useState(false)
  const ref = useRef(null)
  const buttonRef = useRef(null)

  useEffect(() => {
    if (!open) return undefined
    const onDown = (e) => { if (ref.current && !ref.current.contains(e.target)) setOpen(false) }
    const onKey = (e) => {
      if (e.key !== 'Escape') return
      setOpen(false)
      buttonRef.current?.focus()
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <div className="chat-mcp-dropdown chat-addons-dropdown" ref={ref} data-testid={testId}>
      <button
        ref={buttonRef}
        type="button"
        className={`btn btn-sm ${count > 0 ? 'btn-primary' : 'btn-secondary'}`}
        title={title}
        aria-haspopup="true"
        aria-expanded={open}
        aria-describedby={open ? describedBy : undefined}
        data-testid={`${testId}-button`}
        onClick={() => setOpen((v) => !v)}
      >
        <Icon name={icon} /> {label}
        {count > 0 && <span className="chat-mcp-badge" data-testid={`${testId}-count`}>{count}</span>}
      </button>
      {open && (
        <div className={`chat-mcp-dropdown-menu swl-menu ${menuClassName}`.trim()} role="group" aria-label={title} data-testid={`${testId}-menu`}>
          {children}
        </div>
      )}
    </div>
  )
}

// The fixed header: title, summary and the actions; children go under them
// (a search field, notes).
export function SwitchHead({ title, summary, summaryTestId, actions, children }) {
  return (
    <div className="swl-head">
      <div className="swl-head__line">
        <span className="swl-head__title">{title}</span>
        {summary && <span className="swl-head__count" aria-live="polite" data-testid={summaryTestId}>{summary}</span>}
      </div>
      {actions}
      {children}
    </div>
  )
}

// Select all and Disable all. An action with nothing to change is disabled.
// `compact` is the pair of a group heading, named for the group.
export function BulkActions({ onAll, onNone, allDisabled, noneDisabled, allLabel, noneLabel, allAria, noneAria, testId, compact = false }) {
  return (
    <div className={compact ? 'swl-bulk swl-bulk--compact' : 'swl-head__actions'}>
      <Button size="sm" variant="ghost" disabled={allDisabled} onClick={onAll} aria-label={allAria} data-testid={`${testId}-all`}>{allLabel}</Button>
      <Button size="sm" variant="ghost" disabled={noneDisabled} onClick={onNone} aria-label={noneAria} data-testid={`${testId}-none`}>{noneLabel}</Button>
    </div>
  )
}

// A group of rows under a heading with its icon, its count and, when it has
// more than one switch, its own actions.
export function SwitchGroup({ title, icon, count, actions, testId, children }) {
  const headId = useId()
  return (
    <section className="swl-group" aria-labelledby={headId} data-testid={testId}>
      <div className="swl-group__head">
        {icon && <Icon name={icon} className="swl-group__icon" />}
        <span className="swl-group__title" id={headId}>{title}</span>
        {count && <span className="swl-group__count">{count}</span>}
        {actions}
      </div>
      {children}
    </section>
  )
}

// One row: the name (a click on it toggles the switch), a line of text, a
// chip, a meta text on the right and the switch. A row that cannot be used
// has a disabled switch and says why; the reason describes the switch.
//
//   name          the visible name; it names the switch
//   desc          a line of text; on the name's line unless `stacked`
//   tone          warn draws the line as a warning
//   chip          a short label next to the name ("Served by add-on searx")
//   meta          a muted text before the switch ("4 tools")
//   reason        why the switch is disabled (drawn under the name)
//   title         the tooltip of the row; else the name and the text
//   data          data-* attributes of the row
export function SwitchRow({
  name, desc, tone, chip, chipTestId, meta, metaTestId, metaKind, reason, title, checked, disabled = false, onChange,
  stacked = false, testId, descTestId, data = {},
}) {
  const nameId = useId()
  const descId = useId()
  const switchId = useId()
  const line = reason || desc
  const tip = title || (desc ? `${name}: ${desc}` : name)
  const attrs = Object.fromEntries(Object.entries(data).map(([k, v]) => [`data-${k}`, v]))
  return (
    <div
      className={`swl-row${stacked ? ' swl-row--stacked' : ''}`}
      data-on={checked ? 'true' : 'false'}
      data-disabled={disabled ? 'true' : undefined}
      title={title}
      data-testid={testId}
      {...attrs}
    >
      <label className="swl-row__text" htmlFor={switchId} title={title ? undefined : tip}>
        <span className="swl-row__top">
          <span className="swl-row__name" id={nameId} title={title ? name : undefined}>{name}</span>
          {chip && <StatusChip tone="idle" icon="puzzle" className="swl-row__chip" testId={chipTestId}>{chip}</StatusChip>}
        </span>
        {line && (
          <span className={`swl-row__desc${reason ? ' swl-row__desc--reason' : ''}${tone === 'warn' ? ' swl-row__desc--warn' : ''}`} id={descId} data-testid={reason ? undefined : descTestId}>
            {reason && <Icon name="info" className="swl-row__why" />}
            {line}
          </span>
        )}
        {reason && desc && <span className="swl-row__desc" data-testid={descTestId}>{desc}</span>}
      </label>
      {meta && <span className="swl-row__src" data-testid={metaTestId} data-kind={metaKind}>{meta}</span>}
      <Switch
        id={switchId}
        checked={checked}
        disabled={disabled}
        labelledBy={nameId}
        describedBy={line ? descId : undefined}
        onChange={onChange}
      />
    </div>
  )
}

// A row with no switch: a built-in skill that an add-on replaces.
export function SwitchRowStatic({ name, desc, title, testId }) {
  return (
    <div className="swl-row" data-replaced="true" data-testid={testId} title={title}>
      <span className="swl-row__text">
        <span className="swl-row__name">{name}</span>
        {desc && <span className="swl-row__desc">{desc}</span>}
      </span>
    </div>
  )
}

// The bottom line: a refused change, said in a few words with the server's
// message in the tooltip, and what goes after it (a link to manage).
export function SwitchStatus({ error, failed, testId, children }) {
  const { t } = useTranslation('chat')
  return (
    <div className="swl-foot">
      <div className="swl-status" role="alert" data-testid={testId}>
        {error && (
          <>
            <Icon name="warning" />
            <span title={error}>{failed || t('skills.failed')}</span>
          </>
        )}
      </div>
      {children}
    </div>
  )
}
