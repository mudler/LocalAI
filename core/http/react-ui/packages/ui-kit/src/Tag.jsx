import Icon from './Icon.jsx'

// A tag: a short label on an item that is not a state ("alias", "GGUF",
// "Built-in", a capability, a version). The look of the tags in the home model
// picker: 20 px, small radius, muted text on the hover surface. A state
// ("Running", "Failed", "Update available") is a StatusChip, not a tag.
//
//   tone      neutral (default) | accent | ok | warn | error. Colour is never
//             the only signal: the words say what the tag means.
//   icon      a kit icon id drawn before the words
//   mono      the words in the mono font, for ids and versions a person copies
//   title     a tooltip for a tag whose words are short
//   as        the element, span by default (button or a link for a tag that
//             opens something)
export default function Tag({ tone = 'neutral', icon, mono = false, title, className = '', testId, as: El = 'span', children, ...rest }) {
  return (
    <El
      {...rest}
      className={`ui-tag${tone !== 'neutral' ? ` ui-tag--${tone}` : ''}${mono ? ' ui-tag--mono' : ''} ${className}`.trim()}
      title={title}
      data-testid={testId}
    >
      {icon && <Icon name={icon} className="ui-tag__icon" />}
      {children}
    </El>
  )
}
