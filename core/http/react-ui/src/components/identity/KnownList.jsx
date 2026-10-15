import { useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { ageText, initials, labelsText } from '../../utils/identity'
import Icon from '../Icon'

// eslint-disable-next-line no-unused-vars
function Avatar({ entry, tone }) {
  return (
    <span className="idn-avatar" data-tone={tone} aria-hidden="true">
      {entry.thumbnail ? <img src={entry.thumbnail} alt="" /> : initials(entry.name)}
    </span>
  )
}

// eslint-disable-next-line no-unused-vars
function PlaySample({ entry }) {
  const { t } = useTranslation('biometrics')
  const audio = useRef(null)
  if (!entry.sampleUrl) return null
  const play = () => {
    if (!audio.current) audio.current = new Audio(entry.sampleUrl)
    audio.current.currentTime = 0
    audio.current.play().catch(() => {})
  }
  return (
    <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" onClick={play} aria-label={t('known.play', { name: entry.name })}>
      <Icon name="play" />
    </button>
  )
}

// The registry as a table: who is saved in this browser, since when, and
// whether the server still seems to have them. Names and labels are the only
// facts shown; the saved sample, when the person chose to keep one, plays.
export default function KnownList({ entries, extras, missing, waiting, bestId, onDelete, onUndo, onAgain, noun, onEnrol, kind, canEnrol = true }) {
  const { t, i18n } = useTranslation('biometrics')
  const rows = [
    ...entries.map(e => ({ ...e, local: true })),
    ...extras.map(e => ({ ...e, local: false })),
  ]

  if (rows.length === 0) {
    return (
      <div className="idn-known__empty dk-empty" data-testid="known-empty">
        <div className="dk-empty-icon"><Icon name={kind === 'face' ? 'smile' : 'mic'} /></div>
        <h3 className="dk-empty-title">{t(`${kind}.emptyTitle`)}</h3>
        <p className="dk-empty-text">{t(`${kind}.emptyText`)}</p>
        <button type="button" className="dk-btn dk-btn--primary" onClick={() => onEnrol()} disabled={!canEnrol}><Icon name="plus" /> {t(`${kind}.enrol`)}</button>
      </div>
    )
  }

  return (
    <div className="dk-table-wrap dk-table-wrap--flat" role="region" aria-label={t(`${kind}.knownTitle`)} tabIndex={0}>
      <table className="dk-table" data-testid="known-list">
        <caption className="dk-sr-only">{t('known.caption', { noun })}</caption>
        <thead>
          <tr>
            <th scope="col">{t('known.name')}</th>
            <th scope="col" className="dk-hide-phone">{t('known.added')}</th>
            <th scope="col"><span className="dk-sr-only">{t('known.actions')}</span></th>
          </tr>
        </thead>
        <tbody>
          {rows.map(row => {
            const gone = row.local && missing.has(row.id)
            const pending = waiting[row.id]
            const best = row.id === bestId
            return (
              <tr key={row.id} data-row data-selected={best ? 'true' : undefined} data-pending={pending ? 'true' : undefined} data-testid={`known-row-${row.id}`}>
                <td>
                  <span className="idn-who">
                    <Avatar entry={row} tone={gone ? 'warn' : undefined} />
                    <span className="idn-who__text">
                      <span className="dk-table-name">
                        {row.name}
                        {best && <span className="dk-badge dk-badge--ok"><Icon name="check" /> {t('known.best')}</span>}
                        {gone && <span className="dk-badge dk-badge--warn" data-testid="not-on-server"><Icon name="warning" /> {t('known.notOnServer')}</span>}
                        {!row.local && <span className="dk-badge" data-testid="not-in-browser">{t('known.notInBrowser')}</span>}
                        {pending && <span className="dk-badge dk-badge--error">{t('known.removing')}</span>}
                      </span>
                      <span className="dk-table-sub" data-error={gone ? true : undefined}>
                        {gone ? t('known.goneText', { print: noun }) : !row.local ? t('known.extraText') : labelsText(row.labels) || t('known.noLabels')}
                      </span>
                      {gone && !pending && <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm idn-again" onClick={() => onAgain(row)}>{row.sampleUrl || row.thumbnail ? t('known.reenrolSaved') : t('known.enrolAgain')}</button>}
                    </span>
                  </span>
                </td>
                <td className="dk-hide-phone dk-mono idn-when">{row.registeredAt ? ageText(row.registeredAt, Date.now(), i18n.language) : ''}</td>
                <td className="idn-actions">
                  {pending ? (
                    <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => onUndo(row.id)}>{t('known.undo')}</button>
                  ) : (
                    <>
                      <PlaySample entry={row} />
                      <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" onClick={() => onDelete(row)} aria-label={t('known.forget', { name: row.name })}>
                        <Icon name="trash" />
                      </button>
                    </>
                  )}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
