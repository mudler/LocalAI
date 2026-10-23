import { useTranslation } from 'react-i18next'
import { fillStyle } from '../home/memory'
import { gbLabel, gbNumber } from '../../utils/modelLedger'
import Icon from '../Icon'

// What the reply is waiting for while the model comes up. It shows only what the
// page knows: the phase the server names (preparing, installing the backend,
// staging files, loading), the node, the bytes and the remaining time when the
// server reports them. A model that is simply not loaded yet has no phases to
// show, so the card says so and nothing else.
//
// `progress` is { label, progress, detail, sent, total } from the page, or null.
export default function LoadCard({ model, progress }) {
  const { t } = useTranslation('chat')
  const pct = progress?.progress > 0 ? Math.min(100, progress.progress) : 0
  return (
    <div className="cx-load" role="status" data-testid="chat-load">
      <h3>
        <Icon name="spinner" spin />
        <span>{t('load.title')}</span>
        {model && <code>{model}</code>}
      </h3>
      {progress && (
        <ul className="cx-phase">
          <li>
            <Icon name="spinner" spin />
            <span data-testid="chat-load-phase">{progress.label}</span>
            {progress.total > 0 && (
              <small>{t('load.bytes', { sent: gbNumber(progress.sent), total: gbLabel(progress.total) })}</small>
            )}
            {pct > 0 && (
              <div className="cx-bar" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(pct)}>
                <i style={fillStyle(pct)} />
              </div>
            )}
          </li>
        </ul>
      )}
      {pct > 0 && <p className="cx-load__pct" data-testid="chat-load-pct">{Math.round(pct)}%</p>}
      {progress?.detail && <p className="cx-load__detail" data-testid="chat-load-detail">{progress.detail}</p>}
      <p className="cx-load__note">{progress ? t('load.queued') : t('load.cold')}</p>
    </div>
  )
}
