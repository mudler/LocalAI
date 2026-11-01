import Icon from '../Icon'

const TONE_ICON = { info: 'info', ok: 'check-circle', warn: 'warning', error: 'alert-circle' }

// An inline notice inside a page: a tone wash, a 1 px tone edge, an icon
// column, the text and an action slot on the right (it wraps under the text
// on a phone). Use it for something on this page that needs attention; use a
// toast only for the result of an action the person just took.
//
//   tone      info (default) | ok | warn | error
//   title     the first sentence, set in bold; the children follow it
//   actions   buttons (sm) for the slot on the right
//   icon      a kit icon id, to replace the tone's icon
//   role      'alert' for an error the person must know about now
export default function Callout({ tone = 'info', title, icon, actions, role, className = '', testId, children, ...rest }) {
  const t = TONE_ICON[tone] ? tone : 'info'
  return (
    <div className={`ui-callout ui-callout--${t} ${className}`.trim()} role={role} data-testid={testId} {...rest}>
      <Icon name={icon || TONE_ICON[t]} className="ui-callout__icon" />
      <div className="ui-callout__text">
        {title && <strong className="ui-callout__title">{title}</strong>}
        {title && children ? ' ' : null}
        {children}
      </div>
      {actions && <div className="ui-callout__actions">{actions}</div>}
    </div>
  )
}
