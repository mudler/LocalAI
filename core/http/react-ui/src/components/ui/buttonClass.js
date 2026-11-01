// The class list of a button: the kit's dk-btn with its modifiers. Kept
// apart from Button.jsx so markup that cannot use the component (a menu item
// rendered by a library, an add-on page) builds the same classes.
export const BUTTON_VARIANTS = ['primary', 'secondary', 'ghost', 'danger', 'danger-ghost']
export const BUTTON_SIZES = ['md', 'sm', 'lg']

export function buttonClass({ variant = 'secondary', size = 'md', iconOnly = false, className } = {}) {
  return [
    'dk-btn',
    `dk-btn--${BUTTON_VARIANTS.includes(variant) ? variant : 'secondary'}`,
    size === 'sm' && 'dk-btn--sm',
    size === 'lg' && 'dk-btn--lg',
    iconOnly && 'dk-btn--icon',
    className,
  ].filter(Boolean).join(' ')
}
