/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import SideSheet from '../SideSheet'
import { Button } from '../ui'
import ClusterSettings from './ClusterSettings'

// The cluster settings in a side sheet, opened from the Workers page. It is the
// form of the Cluster group of Settings; the link at the foot opens it there.
export default function ClusterSheet({ onClose }) {
  const { t } = useTranslation('cluster')
  return (
    <SideSheet
      title={t('sheet.title')}
      description={t('sheet.description')}
      onClose={onClose}
      closeLabel={t('sheet.close')}
      testId="cluster-sheet"
      labelId="cluster-sheet-title"
      footer={(
        <>
          <Link className="dk-link cl-open" to="/app/settings?group=cluster" onClick={onClose} data-testid="cluster-open-settings">{t('sheet.openSettings')}</Link>
          <Button onClick={onClose}>{t('sheet.done')}</Button>
        </>
      )}
    >
      <ClusterSettings />
    </SideSheet>
  )
}
