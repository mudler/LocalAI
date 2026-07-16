import { memo, useState } from 'react'
import { relativeTime } from '../utils/format'
import { useTranslation } from 'react-i18next'

// ThreeDHistory — sibling of MediaHistory for IndexedDB-backed 3D entries
// (see use3DHistory). Reuses MediaHistory's markup, CSS classes, and testids
// so styling and e2e helpers carry over; the differences are the entry shape
// (input-image thumbnail, quality subtitle) and the Blob-backed source.
// Deliberately a plain vertical list — no showcase/gallery mode.
export default memo(function ThreeDHistory({ entries, selectedId, onSelect, onDelete, onClearAll }) {
  const { t } = useTranslation('media')
  const [expanded, setExpanded] = useState(true)

  return (
    <div className="media-history" data-testid="media-history">
      <div
        className={`collapsible-header ${expanded ? 'open' : ''}`}
        onClick={() => setExpanded(!expanded)}
        style={{ display: 'flex', alignItems: 'center' }}
      >
        <i className="fas fa-chevron-right" />
        <span style={{ flex: 1 }}>{t('history.title')} ({entries.length})</span>
        {entries.length > 0 && (
          <button
            className="media-history-clear-btn"
            title={t('history.clearTitle')}
            onClick={(e) => { e.stopPropagation(); onClearAll() }}
          >
            <i className="fas fa-trash" />
          </button>
        )}
      </div>
      {expanded && (
        <div className="media-history-list">
          {entries.length === 0 ? (
            <div className="media-history-empty">{t('history.empty')}</div>
          ) : (
            entries.map(entry => (
              <div
                key={entry.id}
                className={`media-history-item ${selectedId === entry.id ? 'active' : ''}`}
                onClick={() => onSelect(entry.id)}
                data-testid="media-history-item"
              >
                <div className="media-history-item-thumb">
                  {entry.inputThumb ? (
                    <img src={entry.inputThumb} alt="" />
                  ) : (
                    <i className="fas fa-cube" />
                  )}
                </div>
                <div className="media-history-item-info">
                  <div className="media-history-item-top">
                    <span className="media-history-item-prompt">{entry.name || entry.model}</span>
                    <span className="media-history-item-time">{relativeTime(entry.createdAt)}</span>
                  </div>
                  <div className="media-history-item-model">
                    {entry.params?.quality ? `${entry.params.quality} · ` : ''}{entry.model}
                  </div>
                </div>
                <button
                  className="media-history-item-delete"
                  title={t('history.deleteEntry')}
                  onClick={(e) => { e.stopPropagation(); onDelete(entry.id) }}
                  data-testid="media-history-delete"
                >
                  <i className="fas fa-times" />
                </button>
              </div>
            ))
          )}
        </div>
      )}
    </div>
  )
})
