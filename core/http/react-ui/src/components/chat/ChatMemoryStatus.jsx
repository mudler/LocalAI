// SPDX-License-Identifier: MIT
import { useTranslation } from 'react-i18next'
import { useChatMemory } from '../../hooks/useChatMemory'
import { memoryReason, memoryStatus, memoryRetryTime } from '../../utils/chatMemory'

export default function ChatMemoryStatus({ chatId, tick }) {
  const { t } = useTranslation('chat')
  const { rows, error, retrying, refresh, retry } = useChatMemory(chatId, tick)
  if (!chatId || (!rows.length && !error)) return null
  return (
    <details className="cx-memory" data-testid="chat-memory-status">
      <summary>{t('memoryStatus.title', { count: rows.length })}</summary>
      <div className="cx-memory__body">
        <button type="button" className="cx-link" onClick={refresh}>{t('memoryStatus.refresh')}</button>
        {error && <p role="alert">{t(`memoryStatus.errors.${error}`)}</p>}
        <ul aria-label={t('memoryStatus.label')}>
          {rows.map(row => {
            const next = memoryRetryTime(row.next_retry)
            return <li key={row.id}>
              <strong>{t(`memoryStatus.states.${memoryStatus(row.status)}`)}</strong>
              <span>{t('memoryStatus.destination')} <code>{row.destination}</code></span>
              {row.entry_id && <span>{t('memoryStatus.entry')} <code>{row.entry_id}</code></span>}
              {row.reason && <span>{t(`memoryStatus.reasons.${memoryReason(row.reason)}`)}</span>}
              {next && <span>{t('memoryStatus.nextRetry')} <time dateTime={next.toISOString()}>{next.toLocaleString()}</time></span>}
              {row.retry_eligible === true && <button type="button" className="cx-link" disabled={retrying !== null} aria-label={t('memoryStatus.retryLabel', { destination: row.destination })} onClick={() => retry(row.id)}>{t(retrying === row.id ? 'memoryStatus.retrying' : 'memoryStatus.retry')}</button>}
            </li>
          })}
        </ul>
      </div>
    </details>
  )
}
