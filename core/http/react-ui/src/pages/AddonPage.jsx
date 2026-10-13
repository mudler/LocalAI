/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link, useParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import PageHeader from '../components/PageHeader'
import CatalogEmpty from '../components/catalog/CatalogEmpty'

// The address an Open action leads to: /app/addons/{id}/{page}. This build has
// no add-on page host yet, so the route says so instead of showing a missing
// page. The host replaces this component.
export default function AddonPage() {
  const { t } = useTranslation('addons')
  const { addonId } = useParams()
  return (
    <div className="page ad-page" data-testid="addon-page">
      <PageHeader title={addonId} />
      <CatalogEmpty icon="puzzle" title={t('page.title')} text={t('page.body')}>
        <Link className="dk-btn dk-btn--ghost dk-btn--sm" to="/app/addons">{t('page.back')}</Link>
      </CatalogEmpty>
    </div>
  )
}
