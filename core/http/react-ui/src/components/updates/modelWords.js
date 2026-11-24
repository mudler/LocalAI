import { useTranslation } from 'react-i18next'

// The chip of a model row, as StatusChip props. The vocabulary words where
// they fit (update ready, updating, failed); the model states that are not in
// it get a tone and their own words.
export function modelChip(row, t) {
  switch (row.state) {
    case 'ready':
      return row.edited
        ? { tone: 'warn', icon: 'edit', label: t('model.chip.edited') }
        : { status: 'update-ready' }
    case 'updating': return { status: 'updating' }
    case 'failed': return { status: 'failed' }
    case 'on-hold': return { tone: 'idle', icon: 'lock', label: t('model.chip.held') }
    case 'unknown': return { tone: 'idle', icon: 'circle', label: t('model.chip.unknown') }
    case 'gone': return { tone: 'warn', icon: 'warning', label: t('model.chip.gone') }
    case 'cleanup': return { tone: 'accent', icon: 'history', label: t('model.chip.cleanup') }
    default: return null
  }
}

// The words of an upgrade phase. A phase this build does not know is said as
// the server wrote it.
export function usePhaseLabel() {
  const { t } = useTranslation('updates')
  return (phase) => (phase ? t(`model.phase.${phase}`, { defaultValue: phase }) : t('model.phase.starting'))
}

// The words under the name of a model row.
export function useModelDetail(row) {
  const { t } = useTranslation('updates')
  const phaseLabel = usePhaseLabel()
  if (row.kind !== 'model') return null
  switch (row.state) {
    case 'failed': {
      const message = row.error || t('row.noReason')
      return { tone: 'error', text: row.rolledBack ? t('model.detail.rolledBack', { message }) : t('model.detail.failed', { message }) }
    }
    case 'updating':
      return { tone: 'accent', text: t('model.detail.updating', { phase: phaseLabel(row.phase) }) }
    case 'ready':
      if (row.edited) return { tone: 'warn', text: t('model.detail.edited', { count: row.conflicts }) }
      if (row.configOnly) return { tone: 'muted', text: t('model.detail.configOnly') }
      if (row.files.total > 0) return { tone: 'muted', text: t('model.detail.files', { count: row.files.total }) }
      return null
    case 'on-hold':
      return { tone: 'muted', text: row.hasUpdate ? t('model.detail.heldNewer') : t('model.detail.held') }
    case 'unknown':
      switch (row.reason) {
        case 'legacy-sidecar': return { tone: 'muted', text: t('model.detail.legacy') }
        case 'not-checked': return { tone: 'muted', text: t('model.detail.notChecked') }
        case 'check-failed': return { tone: 'warn', text: t('model.detail.checkFailed', { message: row.error || t('row.noReason') }) }
        default: return { tone: 'warn', text: t('model.detail.unreadable', { message: row.error || t('row.noReason') }) }
      }
    case 'gone':
      return { tone: 'warn', text: t('model.detail.gone', { entry: row.entryName || row.name }) }
    case 'cleanup':
      return { tone: 'muted', text: t('model.detail.cleanup', { count: row.retained }) }
    default:
      return null
  }
}
