import { useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link } from 'react-router-dom'
import Callout from '../ui/Callout'
import Notice from '../ui/Notice'
import StatusChip from '../ui/StatusChip'
import Button from '../ui/Button'
import Popover from '../Popover'
import './commandsnotice.css'

// What a chat that cannot run commands, or runs them on this machine with no
// isolation, tells the person: a notice in the Tools menu and a chip next to
// it, from useCommandsNotice. Hiding the notice hides the chip and leaves one
// quiet line in the Tools menu, so the fact stays where the tools are.

// The body of the notice: the title, one line of reason, and what to do.
export function CommandsNotice({ notice, testId = 'chat-commands-notice' }) {
  const { t } = useTranslation('chats')
  const { info, hidden } = notice
  if (!info) return null
  const title = t(`tools.commands.${info.titleKey || 'title'}`)
  if (hidden) {
    return <p className="cmd-notice__line" data-testid={`${testId}-line`} data-state={info.state}>{title}</p>
  }
  const hide = { label: t('tools.commands.hide'), onClick: notice.hide, ariaLabel: t('tools.commands.hideLabel'), testId: `${testId}-hide` }
  // The server's built-in bash is off and no add-on runs commands: what, why,
  // who can fix it and the fix, as in the Notice pattern.
  if (info.hostOff) {
    return (
      <Notice
        className="cmd-notice"
        severity={info.severity}
        icon="terminal"
        title={title}
        reason={t(`tools.commands.${info.textKey}`)}
        who={t(`tools.commands.${info.whoKey}`)}
        fix={info.link ? { label: t(`tools.commands.${info.link.labelKey}`), to: info.link.to, testId: `${testId}-link` } : null}
        secondary={hide}
        testId={testId}
        data-state={info.state}
      >
        {info.sandboxLink && (
          <span className="ui-notice__extra">
            {t('tools.commands.hostOff.sandboxText')}{' '}
            <Link to={info.sandboxLink.to} data-testid={`${testId}-sandbox`}>{t(`tools.commands.${info.sandboxLink.labelKey}`)}</Link>
          </span>
        )}
      </Notice>
    )
  }
  return (
    <Callout
      className="cmd-notice"
      icon="terminal"
      tone={info.tone === 'warn' ? 'warn' : undefined}
      title={title}
      testId={testId}
      data-state={info.state}
      actions={(
        <>
          {info.link && <Button size="sm" variant="primary" to={info.link.to} data-testid={`${testId}-link`}>{t(`tools.commands.${info.link.labelKey}`)}</Button>}
          <Button size="sm" variant="ghost" onClick={notice.hide} aria-label={t('tools.commands.hideLabel')} data-testid={`${testId}-hide`}>{t('tools.commands.hide')}</Button>
        </>
      )}
    >
      {t(`tools.commands.${info.textKey}`, { reason: info.reason })}
      {info.reason && ['provider-host', 'unknown', 'provider-unavailable'].includes(info.state) && <p>{info.reason}</p>}
      {info.askKey && <> {t(`tools.commands.${info.askKey}`)}</>}
    </Callout>
  )
}

// The chip of the composer's tools area. It opens the same notice in a popover.
export function CommandsChip({ notice }) {
  const { t } = useTranslation('chats')
  const [open, setOpen] = useState(false)
  const anchor = useRef(null)
  const popId = useId()
  if (!notice.info || notice.hidden) return null
  const title = t(`tools.commands.${notice.info.titleKey || 'title'}`)
  const host = notice.info.tone === 'warn'
  return (
    <>
      <button
        type="button"
        ref={anchor}
        className="cmd-chip"
        aria-expanded={open}
        aria-controls={open ? popId : undefined}
        title={t(host ? 'tools.commands.hostChipTitle' : 'tools.commands.chipTitle')}
        aria-label={title}
        onClick={() => setOpen(v => !v)}
        data-testid="chat-commands-chip"
        data-state={notice.info.state}
      >
        <StatusChip tone={host ? 'warn' : 'idle'} icon="terminal">{title}</StatusChip>
      </button>
      <Popover anchor={anchor} open={open} onClose={() => setOpen(false)} ariaLabel={title}>
        <div id={popId} className="cmd-pop">
          <CommandsNotice notice={notice} testId="chat-commands-pop" />
        </div>
      </Popover>
    </>
  )
}
