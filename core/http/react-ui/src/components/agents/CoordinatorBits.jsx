/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import { Button, StatusChip } from '../ui'
import { coordinatorState } from '../../utils/coordinator'
import { formatWhen } from './AgentBits'

// The parts the coordinator page and the administrator's section share: the
// state chip, the lines about wakes and usage, and the confirm of a pause.

const CHIP = {
  paused: { tone: 'warn', icon: 'pause' },
  scheduled: { tone: 'ok', icon: 'clock' },
  idle: { tone: 'idle', icon: 'circle' },
}

// One state: Paused, Scheduled (a next wake is set) or Idle (events only).
export function CoordinatorStateChip({ coordinator, testId = 'coordinator-state' }) {
  const { t } = useTranslation('agents')
  const state = coordinatorState(coordinator)
  return <StatusChip tone={CHIP[state].tone} icon={CHIP[state].icon} testId={testId}>{t(`coordinator.state.${state}`)}</StatusChip>
}

// The schedule as words: "Events only", "Every hour"...
export function wakeLabel(t, seconds) {
  const n = Number(seconds) || 0
  if (n === 0) return t('coordinator.wake.opt.events')
  const key = { 900: '15m', 3600: '1h', 21600: '6h', 86400: '24h' }[n]
  return key ? t(`coordinator.wake.opt.${key}`) : t('coordinator.wake.custom', { minutes: Math.round(n / 60) })
}

export const usageLine = (t, u) => ({
  runs: t('coordinator.usage.runs', { used: u?.runs_today ?? 0, limit: u?.runs_limit ?? 0 }),
  sent: t('coordinator.usage.sent', { used: u?.sent_today ?? 0, limit: u?.send_limit ?? 0 }),
})

export const whenOrDash = (ts) => (ts ? formatWhen(ts) : '—')

// Asks before a pause: it stops everything, and what arrives waits.
export function PauseDialog({ name, pending, onConfirm, onCancel, admin = false }) {
  const { t } = useTranslation('agents')
  return (
    <Dialog
      role="alertdialog"
      title={t('coordinator.pause.title', { name })}
      description={admin ? t('coordinator.pause.adminBody') : t('coordinator.pause.body')}
      onClose={onCancel}
      pending={pending}
      closeLabel={t('create.close')}
      labelId="coordinator-pause-title"
      testId="coordinator-pause-dialog"
      foot={(
        <>
          <Button variant="ghost" data-autofocus onClick={onCancel} disabled={pending}>{t('coordinator.cancel')}</Button>
          <Button variant="danger" icon="pause" busy={pending} onClick={onConfirm} data-testid="coordinator-pause-confirm">{t('coordinator.pause.confirm')}</Button>
        </>
      )}
    >
      <ul className="crd-pause-list">
        <li>{t('coordinator.pause.stopsSending')}</li>
        <li>{t('coordinator.pause.stopsRuns')}</li>
        <li>{t('coordinator.pause.holds')}</li>
        <li>{t('coordinator.pause.resume')}</li>
      </ul>
    </Dialog>
  )
}
