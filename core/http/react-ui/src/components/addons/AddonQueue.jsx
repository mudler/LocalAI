/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import OperationCard from '../OperationCard'
import { Button, Callout } from '../ui'
import { useOperations } from '../../hooks/useOperations'
import { queueRows } from '../../utils/addonQueue'
import { describeError } from './errors'
import '../../pages/operate.css'

// The words for one refusal of the owner. A refusal is a typed answer; the
// owner's own reason is shown when it gave one.
function refusalWords(error, t) {
  if (error && error.type === 'no_answer') return t('queue.noAnswer')
  if (error && error.type === 'not_in_library') return t('queue.notInLibrary')
  return describeError(error && typeof error === 'object' && error.kind ? error : { kind: 'invalid', type: error && error.type, message: error && error.message }, t)
}

// The installs that run in the background: one row per add-on, the same row as
// the model and backend installs of the Activity page. A refusal of the owner
// has no job, so it is a note here until the person closes it.
export default function AddonQueue({ queue }) {
  const { t } = useTranslation('addons')
  const { operations, cancelOperation, dismissFailedOp } = useOperations()
  const rows = queueRows(operations)
  if (rows.length === 0 && queue.notes.length === 0) return null
  return (
    <section className="ad-queue" aria-labelledby="ad-queue-title" data-testid="addon-queue">
      <h2 className="ad-queue__title" id="ad-queue-title">
        {t('queue.title')} <span className="ad-queue__count">{rows.length}</span>
      </h2>
      {queue.notes.map(n => (
        <Callout
          key={n.id}
          tone="error"
          role="alert"
          className="ad-queue__note"
          testId="queue-refused"
          data-addon={n.addonId}
          actions={<Button size="sm" variant="ghost" onClick={() => queue.dismissNote(n.id)}>{t('catalog.close')}</Button>}
        >
          {n.addonId ? t('queue.refused', { name: n.name, reason: refusalWords(n.text, t) }) : refusalWords(n.text, t)}
        </Callout>
      ))}
      {rows.length > 0 && (
        <div className="op-list" data-testid="queue-rows">
          {rows.map(op => (
            <OperationCard
              key={op.jobID || op.id}
              operation={op}
              onCancel={cancelOperation}
              onDismiss={dismissFailedOp}
              onRetry={queue.reviewOp}
              onReview={queue.reviewOp}
            />
          ))}
        </div>
      )}
    </section>
  )
}
