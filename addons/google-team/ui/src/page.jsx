/* eslint-disable no-unused-vars -- JSX */
import { RouteProvider } from 'react-router-dom'
import ConnectorPage from '../../../internal/connector-ui/src/ConnectorPage'
import { WriteModeField, WriteModeChip } from '../../../internal/connector-ui/src/addonparts'
import { registerAddonLocales } from '../../../internal/connector-ui/src/i18n'
import en from './words.en.json'

registerAddonLocales('googleTeam', { en })
const modes = ['off', 'propose', 'auto']
const modeOf = (mode) => modes.includes(mode) ? mode : 'off'
const extraForm = (c) => ({ write_mode: modeOf(c.write_mode) })
const extraBody = (form) => ({ resources: ['team'], write_mode: modeOf(form.write_mode) })
const NoToken = () => null

function Fields({ form, setForm, t }) {
  return <WriteModeField ns="google-team" form={form} setForm={setForm} t={t} modes={modes} modeOf={modeOf} />
}

function RowInfo({ c, t }) {
  return <WriteModeChip ns="google-team" mode={modeOf(c.write_mode)} t={t} />
}

function Help({ t }) {
  return <p className="cn-muted">{t('limits')}</p>
}

export function createTeam() {
  function Component({ page, path, search }) {
    if (page !== 'apps') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <ConnectorPage kind="google-team" ns="googleTeam" testId="google-team-page" TokenFields={NoToken} tokenOf={() => ''} messaging={false} login
          ExtraFields={Fields} extraBody={extraBody} extraForm={extraForm} extraValid={() => true} Help={Help} RowInfo={RowInfo} />
      </RouteProvider>
    )
  }
  return { Component, components: {}, chatActions: [], dispose() {} }
}
