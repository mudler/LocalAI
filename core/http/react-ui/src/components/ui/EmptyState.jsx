import Icon from '../Icon'

// What a region shows when it loaded and has nothing in it. One pattern for
// every page: left-aligned, a title, a body of up to 56 characters a line,
// the primary action (and one ghost "learn" link) on the right.
//
//   variant   first-run (default): a dashed outline; facts or steps allowed
//             no-results: one line and a "Clear filters" action, no outline
//             unavailable: the feature is off or not allowed; muted
//   title     what is empty, as a statement: "No MCP servers yet"
//   body      one or two sentences: what it is for and how to start
//   actions   the primary <Button> and at most one ghost link
//   facts     2-3 short facts: [{ text, ok }] (ok=false draws a cross)
//   steps     numbered steps, only where order matters (Memory, Triggers)
//   icon      a kit icon id drawn in a small tile before the title
//   level     the heading level of the title (default 2)
//
// When a page is empty, the empty state carries the primary action and the
// page header hides its own copy of it.
export default function EmptyState({
  variant = 'first-run', title, body, actions, facts, steps, icon, level = 2,
  className = '', testId, children,
}) {
  const Heading = `h${Math.min(6, Math.max(2, level))}`
  return (
    <section className={`ui-empty ui-empty--${variant} ${className}`.trim()} data-testid={testId}>
      <div className="ui-empty__main">
        {icon && <span className="ui-empty__icon" aria-hidden="true"><Icon name={icon} /></span>}
        <div className="ui-empty__text">
          {title && <Heading className="ui-empty__title">{title}</Heading>}
          {body && <p className="ui-empty__body">{body}</p>}
          {children}
          {steps?.length > 0 && (
            <ol className="ui-empty__steps">
              {steps.map((s, i) => <li key={i}>{s}</li>)}
            </ol>
          )}
          {facts?.length > 0 && (
            <ul className="ui-empty__facts">
              {facts.map((f, i) => (
                <li key={i} className={f.ok === false ? 'is-no' : undefined}>
                  <Icon name={f.ok === false ? 'close' : 'check'} />
                  {f.text}
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
      {actions && <div className="ui-empty__actions">{actions}</div>}
    </section>
  )
}
