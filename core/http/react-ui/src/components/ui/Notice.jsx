import Button from './Button'
import Callout from './Callout'
import { NOTICE_ICONS, noticeAction, noticeRole, noticeSeverity } from './noticeModel'

// "This is off or not available, why, and the fix." One notice for every such
// state, in one order: what, why, who can fix it, the fix. It extends Callout,
// so the look is the product's own.
//
//   severity   info (off by design) | warn (degraded, or an action is
//              recommended) | error (something the person asked for does not
//              work). Sets the colour, the icon and the live role.
//   title      what is off or unavailable; short, no trailing period
//   reason     why, in one plain sentence; the server's own words when it has
//              them, never a status code
//   who        who can fix it ("Ask an administrator of this server.")
//   fix        the one primary action: { label, onClick | to | href, busy,
//              disabled, testId, ariaLabel }. The label is a verb that names
//              the result ("Open the setting", "Upgrade").
//   secondary  an optional second action of the same shape; variant 'ghost'
//              (default) or 'secondary'. A dismiss ("Hide") goes here.
//   icon       a kit icon id, to replace the severity's icon
//   children   anything that belongs after the who line (a link)
//
// A notice with no fix is a plain statement: the person has nothing to do.
export default function Notice({ severity, title, reason, who, fix, secondary, icon, className = '', testId, children, ...rest }) {
  const level = noticeSeverity(severity)
  const primary = noticeAction(fix)
  const second = noticeAction(secondary)
  const draw = (action, variant) => (
    <Button
      size="sm"
      variant={action.variant || variant}
      to={action.to}
      href={action.href}
      onClick={action.onClick}
      busy={action.busy}
      disabled={action.disabled}
      aria-label={action.ariaLabel}
      data-testid={action.testId}
    >
      {action.label}
    </Button>
  )
  const actions = primary || second ? (
    <>
      {primary && draw(primary, 'primary')}
      {second && draw(second, 'ghost')}
    </>
  ) : null
  return (
    <Callout
      tone={level}
      icon={icon || NOTICE_ICONS[level]}
      role={noticeRole(level)}
      className={`ui-notice ${className}`.trim()}
      title={title}
      actions={actions}
      testId={testId}
      data-severity={level}
      {...rest}
    >
      {reason && <span className="ui-notice__reason">{reason}</span>}
      {who && <span className="ui-notice__who">{who}</span>}
      {children}
    </Callout>
  )
}
