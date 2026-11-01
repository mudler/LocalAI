import Icon from './Icon'

// The question above the page before a link of the page opens. The site (the
// last two labels of the host) is what to look at, so it is the large part;
// a host in punycode also says how it reads, since it may imitate another
// site. Nothing has the focus: a page cannot make Enter open a link.
//
// A Home widget uses the same bar for its item links, with its own label.
export default function PageLinkAsk({ link, onOpen, onCancel, t, label }) {
  const { href, host, display, site, lookalike } = link.target
  const sub = host.length > site.length ? host.slice(0, host.length - site.length) : ''
  const title = label || t('link.label')
  return (
    <div className="pg-ask pg-link" role="group" aria-label={title} aria-live="polite" data-testid="page-link-ask">
      <span className="pg-ask__label">{title}</span>
      <span className="pg-link__target">
        <Icon name="external-link" className="pg-link__icon" />
        <span className="pg-link__host" data-testid="page-link-host">
          {sub && <span className="pg-link__sub">{sub}</span>}
          <strong className="pg-link__site">{site}</strong>
        </span>
      </span>
      <code className="pg-link__url" title={href}>{href}</code>
      {lookalike && (
        <span className="pg-link__warn" role="note" data-testid="page-link-lookalike">
          <Icon name="warning" /> {t('link.lookalike', { display })}
        </span>
      )}
      <span className="pg-ask__actions">
        <button type="button" className="btn btn-primary btn-sm" onClick={onOpen} data-testid="page-link-open">
          {t('link.open', { site })}
        </button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={onCancel} data-testid="page-link-cancel">{t('link.cancel')}</button>
      </span>
    </div>
  )
}
