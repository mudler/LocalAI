import { forwardRef } from 'react'
import Icon from './Icon.jsx'
import { buttonClass } from './buttonClass.js'

// The one button. It draws the kit's button (dk-btn), so a <Button> and a
// hand-written `dk-btn` or legacy `btn` look the same. See README.md.
//
//   variant   primary | secondary (default) | ghost | danger | danger-ghost
//   size      md (default, --ctl-h) | sm (--ctl-h-sm) | lg
//   icon      kit icon id drawn before the label (16 px)
//   iconEnd   kit icon id drawn after the label
//   busy      keeps the width, shows a spinner, sets aria-busy
//   to        a router path: renders `linkComponent` with `to`, or a plain
//             <a href> when no link component is given
//   href      an address: renders an <a>
//   linkComponent  the app's router link (react-router's Link, for example).
//             The kit has no router; the app passes its own. The web UI's
//             components/ui/Button passes it for every button.
//
// A button with an icon and no children is a square icon button; it needs an
// aria-label.
const Button = forwardRef(function Button({
  variant = 'secondary', size = 'md', icon, iconEnd, busy = false, disabled = false,
  to, href, linkComponent: LinkComponent, type = 'button', className, children, onClick, ...rest
}, ref) {
  const iconOnly = !!icon && (children === undefined || children === null || children === false)
  const classes = buttonClass({ variant, size, iconOnly, className })
  // Busy with an icon swaps the icon for the spinner; busy without one hides
  // the label under a centred spinner. Either way the width does not move.
  const busyMode = busy ? (icon ? 'icon' : 'label') : undefined
  const content = (
    <>
      {icon && <Icon name={busy ? 'spinner' : icon} spin={busy} className="ui-btn__icon" />}
      {!iconOnly && children !== undefined && <span className="ui-btn__label">{children}</span>}
      {iconEnd && !iconOnly && <Icon name={iconEnd} className="ui-btn__icon" />}
    </>
  )
  const state = {
    'aria-busy': busy || undefined,
    'data-busy': busyMode,
  }

  if (to !== undefined || href !== undefined) {
    const off = disabled || busy
    const linkProps = {
      ref, className: classes, ...state, ...rest,
      'aria-disabled': off || undefined,
      tabIndex: off ? -1 : rest.tabIndex,
      onClick: off ? (e) => e.preventDefault() : onClick,
    }
    if (to !== undefined && LinkComponent) return <LinkComponent to={to} {...linkProps}>{content}</LinkComponent>
    return <a href={to !== undefined ? to : href} {...linkProps}>{content}</a>
  }
  return (
    <button ref={ref} type={type} className={classes} disabled={disabled} onClick={busy ? undefined : onClick} {...state} {...rest}>
      {content}
    </button>
  )
})

export default Button
