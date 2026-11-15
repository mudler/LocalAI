import { useTranslation } from 'react-i18next'
import KitLoadError from '../../../packages/ui-kit/src/LoadError.jsx'

// The kit's LoadError (packages/ui-kit) in the person's language.
// Props: see the kit file.
export default function LoadError(props) {
  const { t } = useTranslation('common')
  const labels = {
    title: t('ui.loadError.title'),
    tryAgain: t('ui.tryAgain'),
    nothingChanged: t('ui.loadError.nothingChanged'),
    serverSaid: (message) => t('ui.loadError.serverSaid', { message }),
  }
  return <KitLoadError {...props} labels={labels} />
}
