import { useTranslation } from 'react-i18next'

// The favourite star. The kit's icon set has no star, so it is drawn here; it
// is one path and takes its colour from the text colour like every other icon.
export function StarGlyph({ className = '' }) {
  return (
    <svg className={`studio-star__glyph ${className}`.trim()} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <path d="M12 3.6l2.6 5.5 6 .8-4.4 4.2 1.1 6-5.3-2.9-5.3 2.9 1.1-6L3.4 9.9l6-.8z" />
    </svg>
  )
}

export default function StarButton({ on, onToggle, className = '' }) {
  const { t } = useTranslation('media')
  return (
    <button
      type="button"
      className={`studio-star ${className}`.trim()}
      aria-pressed={on}
      aria-label={t('studio.work.favourite')}
      title={t('studio.work.favourite')}
      data-testid="studio-star"
      onClick={(e) => { e.stopPropagation(); e.preventDefault(); onToggle() }}
    >
      <StarGlyph />
    </button>
  )
}
