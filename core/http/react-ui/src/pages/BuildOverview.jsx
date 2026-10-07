// Link and PageHeader are used in JSX only, which eslint cannot see.
// eslint-disable-next-line no-unused-vars
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import PageHeader from '../components/PageHeader'
import Icon from '../components/Icon'
import { buildHub, isTabVisible, visibleItems } from '../components/hub/hubConfig'
import { useHubAuth } from '../components/hub/useHubAuth'
import { preloadRoute } from '../router'

// The Build landing page: every tool the viewer can use, one line each. The
// list comes from the hub config, so a tool hidden by a feature gate or a
// permission is hidden here as well.
export default function BuildOverview() {
  const { t } = useTranslation('nav')
  const auth = useHubAuth()
  const tools = buildHub.tabs.filter(tab => tab.id !== 'overview' && isTabVisible(tab, auth))

  return (
    <div className="page page--medium">
      <PageHeader title={t('sections.build')} supporting={t('hub.overviewSupporting')} />
      {tools.length === 0 ? (
        <p className="build-overview__empty">{t('hub.overviewEmpty')}</p>
      ) : (
        <ul className="build-overview" aria-label={t('hub.toolsLabel')}>
          {tools.map(tool => {
            const path = visibleItems(tool, auth)[0].path
            return (
              <li key={tool.id}>
                <Link
                  className="dk-row"
                  to={path}
                  onMouseEnter={() => preloadRoute(path)}
                  onFocus={() => preloadRoute(path)}
                >
                  <span className="dk-row-lead"><Icon name={tool.icon} aria-hidden="true" /></span>
                  <span className="dk-row-main">
                    <span className="dk-row-title">{t(tool.labelKey)}</span>
                    <span className="dk-row-meta">{t(tool.descriptionKey)}</span>
                  </span>
                  <span className="dk-row-end"><Icon name="chevron-right" aria-hidden="true" /></span>
                </Link>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
