// The page frame: one left edge, one top padding and one of two widths.
//
//   width   content (default): --page-max-content, 1120 px; forms, lists,
//           settings and most pages
//           full: no cap; data tables, Models, Chat
//
// It renders the existing .page wrapper with a width modifier, so pages that
// already use .page keep their padding and gutter.
export default function Page({ width = 'content', as: Tag = 'div', className = '', children, ...rest }) {
  const w = width === 'full' ? 'full' : 'content'
  return (
    <Tag className={`page page--${w} ${className}`.trim()} {...rest}>
      {children}
    </Tag>
  )
}
