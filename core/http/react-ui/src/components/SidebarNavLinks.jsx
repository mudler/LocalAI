/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { createPortal } from 'react-dom'
import { NavLink } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from './Icon'
import PageLinkAsk from './PageLinkAsk'
import { pageLinkTarget } from '../utils/pageLinks'
import '../pages/pages.css'

// A custom link of the sidebar (a nav.json item of type page, chat or url).
// A page or a chat opens inside the app. An address leaves it, so it asks
// first, with the same bar as the links of a page: the site is the large part
// and Open is a click in this document. The bar is drawn over the page, not
// inside the rail, where it would not fit when the rail is collapsed.
export default function SidebarNavLink({ item, label, icon, onClose, collapsed }) {
  const { t } = useTranslation('nav')
  const { t: tp } = useTranslation('pages')
  const [ask, setAsk] = useState(null)

  if (item.type === 'url') {
    const target = pageLinkTarget(item.url)
    if (target.error) return null
    const open = () => {
      window.open(target.href, '_blank', 'noopener,noreferrer')
      setAsk(null)
      onClose?.()
    }
    return (
      <>
        <button
          type="button" className="nav-item nav-item--link" onClick={() => setAsk({ target })}
          title={collapsed ? label : t('navLinkExternal', { site: target.site })}
          data-testid="nav-custom-link" data-kind="url"
        >
          <Icon name={icon} className="nav-icon" aria-hidden="true" />
          <span className="nav-label">{label || t('untitledChat')}</span>
          <Icon name="external-link" className="nav-external" aria-hidden="true" />
        </button>
        {ask && createPortal(
          <div className="nav-linkask" data-testid="nav-link-ask">
            <PageLinkAsk link={ask} t={tp} label={t('navLinkAsk')} onOpen={open} onCancel={() => setAsk(null)} />
          </div>,
          document.body,
        )}
      </>
    )
  }

  const to = item.type === 'page' ? `/app/pages/${encodeURIComponent(item.page)}` : `/app/chats/${encodeURIComponent(item.chat)}`
  return (
    <NavLink
      to={to} onClick={onClose}
      className={({ isActive }) => `nav-item${isActive ? ' active' : ''}`}
      title={collapsed ? label : undefined}
      data-testid="nav-custom-link" data-kind={item.type}
    >
      <Icon name={icon} className="nav-icon" aria-hidden="true" />
      <span className="nav-label">{label || t('untitledChat')}</span>
    </NavLink>
  )
}
