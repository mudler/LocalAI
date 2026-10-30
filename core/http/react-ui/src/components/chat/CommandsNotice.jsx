import { useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Callout from '../ui/Callout'
import StatusChip from '../ui/StatusChip'
import Button from '../ui/Button'
import Popover from '../Popover'
import './commandsnotice.css'

// What a chat that cannot run commands tells the person: a notice in the Tools
// menu and a chip next to it, from useCommandsNotice. Hiding the notice hides
// the chip and leaves one quiet line in the Tools menu, so the fact stays where
// the tools are.

// The body of the notice: the title, one line of reason, and what to do.
export function CommandsNotice({ notice, testId = 'chat-commands-notice' }) {
  const { t } = useTranslation('chats')
  const { info, hidden, hide } = notice
  if (!info) return null
  if (hidden) {
    return <p className="cmd-notice__line" data-testid={`${testId}-line`}>{t('tools.commands.title')}</p>
  }
  return (
    <Callout
      className="cmd-notice"
      icon="terminal"
      title={t('tools.commands.title')}
      testId={testId}
      actions={(
        <>
          {info.link && <Button size="sm" variant="primary" to={info.link.to} data-testid={`${testId}-link`}>{t(`tools.commands.${info.link.labelKey}`)}</Button>}
          <Button size="sm" variant="ghost" onClick={hide} aria-label={t('tools.commands.hideLabel')} data-testid={`${testId}-hide`}>{t('tools.commands.hide')}</Button>
        </>
      )}
    >
      {t(`tools.commands.${info.textKey}`, { reason: info.reason })}
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
  return (
    <>
      <button
        type="button"
        ref={anchor}
        className="cmd-chip"
        aria-expanded={open}
        aria-controls={open ? popId : undefined}
        title={t('tools.commands.chipTitle')}
        aria-label={t('tools.commands.title')}
        onClick={() => setOpen(v => !v)}
        data-testid="chat-commands-chip"
      >
        <StatusChip tone="idle" icon="terminal">{t('tools.commands.title')}</StatusChip>
      </button>
      <Popover anchor={anchor} open={open} onClose={() => setOpen(false)} ariaLabel={t('tools.commands.title')}>
        <div id={popId} className="cmd-pop">
          <CommandsNotice notice={notice} testId="chat-commands-pop" />
        </div>
      </Popover>
    </>
  )
}
