/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import SourcesView from '../catalog/SourcesView'
import { describeError } from './errors'

// The libraries add-ons are installed from. The generic view does the work;
// this only supplies the add-ons adapter and the add-ons wording.
export default function LibrariesView({ adapter }) {
  const { t } = useTranslation('addons')
  return <SourcesView adapter={adapter} describeError={e => describeError(e, t)} />
}
