import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import Notice from '../ui/Notice'
import Button from '../ui/Button'
import { useHarnessFix } from '../../hooks/useHarnessFix'
import { harnessProblem, modelsWithWindow } from '../../utils/chatHarness'
import { formatBytes } from '../../utils/format'
import './harnessbanner.css'

// The banner of a chat whose model has a window that is too small for the chat
// agent (the `harness` block of the chat, see utils/chatHarness.js). Nothing
// shows when the chat is fine. One banner, and what it offers depends on who
// reads it:
//
//   an administrator  sees whether the recommended window fits the memory of
//                     this machine, and one button that sets the window of the
//                     model (the largest that fits and reaches the minimum).
//                     When nothing fits they are told to pick a smaller model.
//   anyone else       can only move the chat to an installed model with a large
//                     enough window, or ask an administrator. They never see a
//                     control for the model's settings; the server refuses that
//                     request from them anyway.
//   the chat's own    a chat setting below the minimum is the owner's to change,
//   setting           so the button gives the chat the window of its model back.
export default function HarnessBanner({ harness, model, models, onSetModelWindow, onSwitchModel, onResetChatWindow }) {
  const { t } = useTranslation('chats')
  const [busy, setBusy] = useState(false)
  const problem = harnessProblem(harness)
  const fromModel = problem?.source !== 'chat'
  const adminFix = !!problem && fromModel && problem.can_fix
  const fix = useHarnessFix({ enabled: adminFix, model, minimum: problem?.minimum, recommended: problem?.recommended })
  if (!problem) return null

  const vars = { window: problem.window, minimum: problem.minimum, recommended: problem.recommended }
  const run = async (action) => {
    setBusy(true)
    try { await action() } finally { setBusy(false) }
  }

  // The chat's own context size setting is too low: the owner can undo it.
  if (!fromModel) {
    return (
      <Notice
        className="cx-harness"
        severity="warn"
        title={t('harness.chat.title')}
        reason={t('harness.chat.reason', vars)}
        fix={{ label: t('harness.chat.fix'), onClick: () => run(onResetChatWindow), busy, testId: 'chat-harness-reset' }}
        testId="chat-harness"
        data-role="owner"
      />
    )
  }

  if (adminFix) {
    const pick = fix.pick
    let memory = null
    let action = null
    if (fix.status === 'loading' || !pick) {
      memory = t('harness.admin.checking', { target: problem.recommended })
    } else if (pick.kind === 'fits') {
      memory = pick.wanted
        ? t('harness.admin.fits', { target: pick.value, need: formatBytes(pick.bytes) })
        : t('harness.admin.fitsLess', { recommended: problem.recommended, target: pick.value, need: formatBytes(pick.bytes) })
      action = pick.value
    } else if (pick.kind === 'unknown') {
      memory = t('harness.admin.unknown')
      action = pick.value
    } else {
      memory = t('harness.admin.none', { minimum: problem.minimum })
    }
    return (
      <Notice
        className="cx-harness"
        severity="warn"
        title={t('harness.title')}
        reason={t('harness.admin.reason', vars)}
        fix={action ? { label: t('harness.admin.fix', { target: action }), onClick: () => run(() => onSetModelWindow(action)), busy, testId: 'chat-harness-fix' } : null}
        testId="chat-harness"
        data-role="admin"
      >
        <span className="ui-notice__who" data-testid="chat-harness-memory">{memory}</span>
      </Notice>
    )
  }

  const options = modelsWithWindow(models, { minimum: problem.minimum, current: model }).slice(0, 3)
  return (
    <Notice
      className="cx-harness"
      severity="warn"
      title={t('harness.title')}
      reason={t('harness.user.reason', vars)}
      who={options.length ? t('harness.user.switch') : t('harness.user.askAdmin')}
      testId="chat-harness"
      data-role="user"
    >
      {options.length > 0 && (
        <span className="cx-harness__options" data-testid="chat-harness-options">
          {options.map(o => (
            <Button
              key={o.id}
              size="sm"
              variant="secondary"
              busy={busy}
              onClick={() => run(() => onSwitchModel(o.id))}
              data-testid={`chat-harness-switch-${o.id}`}
            >
              {t('harness.user.use', { model: o.id, window: o.window })}
            </Button>
          ))}
        </span>
      )}
    </Notice>
  )
}
