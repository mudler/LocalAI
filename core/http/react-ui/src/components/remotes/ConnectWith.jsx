/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import Segmented from '../ui/Segmented'

// How "connect a remote" reaches the other side: the address of a server or
// service, or an invite link from another LocalAI (over P2P). The choice is
// locked while a connect runs and once a remote is saved.
export default function ConnectWith({ value, onChange, locked = false }) {
  const { t } = useTranslation('p2p')
  const id = useId()
  return (
    <div className="dk-field p2p-connect-with">
      <span className="dk-label" id={id}>{t('invite.connectWith')}</span>
      <fieldset disabled={locked}>
        <Segmented
          mode="radio" labelledBy={id} value={value} onChange={locked ? () => {} : onChange}
          options={[
            { id: 'address', label: t('invite.withAddress') },
            { id: 'invite', label: t('invite.withInvite') },
          ]}
          testId="remote-connect-with" itemTestId="remote-connect-with-"
        />
      </fieldset>
    </div>
  )
}
