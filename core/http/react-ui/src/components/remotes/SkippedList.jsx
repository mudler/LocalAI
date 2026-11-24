import { useTranslation } from 'react-i18next'
import { skipReasonKey } from '../../utils/remotes'

// The models a create, an adopt or a refresh left out, as a count that opens
// to a list: the id and why. A reason the page has no words for shows as the
// server sent it.
//
//   skipped   [{ id, reason }]
export default function SkippedList({ skipped }) {
  const { t } = useTranslation('remotes')
  if (!skipped || skipped.length === 0) return null
  return (
    <details className="remotes-skipped" data-testid="remote-skipped">
      <summary className="remotes-skipped__summary">{t('skipped.summary', { count: skipped.length })}</summary>
      <ul className="remotes-skipped__list">
        {skipped.map(s => {
          const key = skipReasonKey(s.reason)
          return (
            <li key={s.id} className="remotes-skipped__item" data-testid="remote-skipped-item">
              <span className="remotes-model__id">{s.id}</span>
              <span className="remotes-hint">{key ? t(`skipped.reason.${key}`) : s.reason}</span>
            </li>
          )
        })}
      </ul>
    </details>
  )
}
