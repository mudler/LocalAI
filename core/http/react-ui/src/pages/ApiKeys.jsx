/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import ApiKeysPanel from '../components/users/ApiKeysPanel'
import AdminPage from '../components/admin/AdminPage'
import './access.css'

// The signed-in administrator's own API keys: make one, see it once, pause it,
// revoke it. LocalAI lists a person's keys only to that person, so another
// person's keys are not here; the Users page shows how many each holds.
export default function ApiKeys() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('admin')
  const { t: ta } = useTranslation('auth')
  return (
    <AdminPage
      className="us-page"
      data-testid="api-keys-page"
      title={t('apiKeys.title')}
      supporting={t('apiKeys.subtitle')}
    >
      <ApiKeysPanel addToast={addToast} intro={ta('keys.adminIntro')} />
    </AdminPage>
  )
}
