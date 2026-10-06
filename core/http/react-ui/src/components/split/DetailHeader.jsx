import Icon from '../Icon'
// DetailHeader is the top of the pane once something is selected: the way back
// out, what you are looking at, and what you can do to it.
//
// The back control is the piece the expand-row never had. Selection lives in
// the URL on all three surfaces, so leaving the detail is a real navigation
// rather than a second click on the thing you just opened.
export default function DetailHeader({
  icon, name, lede, ledeTitle, actions, onBack, backLabel, warning,
  testId = 'detail',
}) {
  return (
    <>
      {onBack && (
        <button type="button" className="detail-pane__back" onClick={onBack} data-testid={`${testId}-back`}>
          <Icon name="arrow-left" /> {backLabel}
        </button>
      )}

      <div className="detail-pane__head">
        {icon && <Icon name={icon} className="detail-pane__icon" />}
        <div className="detail-pane__title">
          <h2 className="detail-pane__name">{name}</h2>
          {lede && (
            // Capped by CSS, with the whole of it on the title so nothing is
            // lost to the truncation.
            <p className="detail-pane__lede" title={ledeTitle || undefined}>{lede}</p>
          )}
        </div>
        {actions && <div className="detail-pane__actions">{actions}</div>}
      </div>

      {warning && (
        <p className="detail-pane__warning">
          <Icon name="alert-circle" /> {warning}
        </p>
      )}
    </>
  )
}
