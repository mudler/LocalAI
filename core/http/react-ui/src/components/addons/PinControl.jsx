import { useTranslation } from 'react-i18next'

// The pin control of an add-on, on its tile and in its detail view: a button
// with a visible word and a pin that is filled when the add-on is pinned. The
// accessible name keeps the add-on's name so a list of them stays unambiguous.
export default function PinControl({ name, pinned, onToggle, variant = 'tile' }) {
  const { t } = useTranslation('addons')
  return (
    <button
      type="button"
      className={variant === 'tile' ? 'ad-tile__pin' : 'dk-btn dk-btn--sm ad-pin'}
      data-testid="pin-toggle"
      data-pinned={pinned ? 'true' : 'false'}
      aria-pressed={pinned}
      aria-label={pinned ? t('drawer.unpin', { name }) : t('drawer.pin', { name })}
      onClick={onToggle}
    >
      <svg className="ad-pin__icon lai-icon" viewBox="0 0 24 24" focusable="false" aria-hidden="true"
        fill={pinned ? 'currentColor' : 'none'} stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
        <path d="M9 3h6l-1 6 3 3v2H7v-2l3-3zM12 14v7" />
      </svg>
      <span className="ad-pin__label">{pinned ? t('drawer.pinned') : t('drawer.pinLabel')}</span>
    </button>
  )
}
