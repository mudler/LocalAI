/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, Callout, StatusChip, Switch } from '../ui'
import Icon from '../Icon'
import { RELAY_MODES, modeChoices, parsePeerList } from './p2pSettingsModel'

const AUTH_DOCS = 'https://localai.io/administration/authentication/'

// The three P2P modes as radio cards. A mode applies at once: the server
// restarts the P2P layer. Mode share needs authentication, so without it the
// card is shown but cannot be chosen.
function ModeChoice({ mode, authEnabled, busy, onChange }) {
  const { t } = useTranslation('p2psettings')
  const titleId = useId()
  return (
    <div className="p2ps-section" data-testid="p2p-mode">
      <div>
        <h3 className="st-row__label" id={titleId}>{t('network.mode.title')}</h3>
        <p className="st-row__desc">{t('network.mode.lede')}</p>
      </div>
      <div className="p2ps-modes" role="radiogroup" aria-labelledby={titleId}>
        {modeChoices(authEnabled).map(choice => {
          const on = mode === choice.id
          return (
            <label key={choice.id} className="p2ps-mode" data-on={on || undefined} data-disabled={choice.disabled || undefined} data-testid={`p2p-mode-${choice.id}`}>
              <input
                type="radio" className="dk-radio" name="p2p-mode" value={choice.id}
                checked={on} disabled={choice.disabled || busy}
                onChange={() => onChange(choice.id)}
              />
              <span className="p2ps-mode__text">
                <span className="p2ps-mode__title">
                  {t(`modes.${choice.id}.title`)}
                  {choice.needsAuth && <StatusChip tone="warn" dot>{t('network.mode.needsAuth')}</StatusChip>}
                </span>
                <span className="p2ps-mode__body">{t(`modes.${choice.id}.body`)}</span>
              </span>
            </label>
          )
        })}
      </div>
      <p className="st-row__meta"><span className="p2ps-key">p2p_mode</span><span>{t('network.mode.replaces')}</span></p>
    </div>
  )
}

// In mode connect: how to share this instance as well. Without
// authentication the switch is not offered; the card says what to turn on.
function ShareFromHere({ authEnabled, busy, onSwitch }) {
  const { t } = useTranslation('p2psettings')
  return (
    <section className="p2ps-share" data-testid="p2p-share-from-here" aria-labelledby="p2ps-share-title">
      <span className="p2ps-share__icon"><Icon name="share" /></span>
      <div className="p2ps-share__text">
        <h3 className="p2ps-share__title" id="p2ps-share-title">{t('share.title')}</h3>
        <p className="p2ps-share__body">{t('share.body')}</p>
        {authEnabled
          ? <p className="p2ps-share__body">{t('share.authOn')}</p>
          : (
            <p className="p2ps-share__auth">
              <StatusChip tone="warn" dot>{t('network.mode.needsAuth')}</StatusChip>
              <a className="dk-link" href={AUTH_DOCS} target="_blank" rel="noopener noreferrer">{t('share.turnOnAuth')}</a>
            </p>
          )}
      </div>
      <Button variant={authEnabled ? 'primary' : 'secondary'} disabled={!authEnabled || busy} busy={busy} onClick={onSwitch} data-testid="p2p-switch-share">
        {t('share.switch')}
      </Button>
    </section>
  )
}

// A setting that applies at once, with a switch at the end of the row.
function SwitchRow({ id, label, desc, settingKey, meta, checked, disabled, onChange }) {
  const labelId = `${id}-label`
  const descId = `${id}-desc`
  return (
    <div className="st-row" data-bool data-field={settingKey}>
      <div className="st-row__main">
        <div className="st-row__title"><span className="st-row__label" id={labelId}>{label}</span></div>
        <p className="st-row__desc" id={descId}>{desc}</p>
        <p className="st-row__meta"><span className="p2ps-key">{settingKey}</span>{meta && <span>{meta}</span>}</p>
      </div>
      <div className="st-row__control">
        <Switch checked={checked} onChange={onChange} labelledBy={labelId} describedBy={descId} disabled={disabled} />
      </div>
    </div>
  )
}

// The network settings of the P2P layer. Each switch applies at once; the
// lists and numbers apply with their own Save button.
export default function NetworkSection({ settings, authEnabled, saving, onSave }) {
  const { t } = useTranslation('p2psettings')
  const mode = settings.p2p_mode
  const share = mode === 'share'
  const busy = Boolean(saving)
  const [peers, setPeers] = useState(settings.p2p_bootstrap_peers.join('\n'))
  const [rate, setRate] = useState(String(Math.round(settings.p2p_relay_rate_limit / 1024)))
  // Keyed on the content, not the array: every reload of the settings brings a new array, and that must not wipe what the person is typing.
  const savedPeers = settings.p2p_bootstrap_peers.join('\n')
  useEffect(() => { setPeers(savedPeers) }, [savedPeers])
  useEffect(() => { setRate(String(Math.round(settings.p2p_relay_rate_limit / 1024))) }, [settings.p2p_relay_rate_limit])

  const peerList = parsePeerList(peers)
  const peersChanged = peerList.join('\n') !== settings.p2p_bootstrap_peers.join('\n')
  const rateValid = /^\d+$/.test(rate.trim())
  const rateChanged = rateValid && Number(rate) * 1024 !== settings.p2p_relay_rate_limit

  return (
    <section className="p2ps-section" aria-labelledby="p2ps-network-title" data-testid="p2p-network">
      <h2 className="st-h2" id="p2ps-network-title">{t('network.title')}</h2>
      <ModeChoice mode={mode} authEnabled={authEnabled} busy={busy} onChange={(m) => onSave({ p2p_mode: m })} />
      {mode === 'connect' && <ShareFromHere authEnabled={authEnabled} busy={saving === 'p2p_mode'} onSwitch={() => onSave({ p2p_mode: 'share' })} />}

      {mode !== 'off' && (
        <div className="st-list">
          <SwitchRow
            id="p2ps-dht" settingKey="p2p_public_dht" label={t('network.dht.title')} desc={t('network.dht.desc')}
            meta={t('network.defaultOn')} checked={settings.p2p_public_dht} disabled={busy}
            onChange={(v) => onSave({ p2p_public_dht: v })}
          />
          <SwitchRow
            id="p2ps-mdns" settingKey="p2p_mdns" label={t('network.mdns.title')} desc={t('network.mdns.desc')}
            meta={t('network.defaultOn')} checked={settings.p2p_mdns} disabled={busy}
            onChange={(v) => onSave({ p2p_mdns: v })}
          />
          <div className="p2ps-edit" data-field="p2p_bootstrap_peers">
            <label className="st-row__label" htmlFor="p2ps-bootstrap">{t('network.bootstrap.title')}</label>
            <p className="st-row__desc" id="p2ps-bootstrap-desc">{t('network.bootstrap.desc')}</p>
            <textarea
              id="p2ps-bootstrap" className="dk-textarea dk-input--mono" rows={3} value={peers}
              placeholder="/dnsaddr/bootstrap.example.org/p2p/12D3KooW…" aria-describedby="p2ps-bootstrap-desc"
              onChange={(e) => setPeers(e.target.value)}
            />
            <div className="p2ps-edit__row">
              <span className="p2ps-key">p2p_bootstrap_peers</span>
              <Button size="sm" disabled={!peersChanged || busy} busy={saving === 'p2p_bootstrap_peers'} onClick={() => onSave({ p2p_bootstrap_peers: peerList })} data-testid="p2p-bootstrap-save">
                {t('actions.save')}
              </Button>
            </div>
          </div>
          {share && (
            <>
              <SwitchRow
                id="p2ps-relay-paired" settingKey="p2p_relay_paired" label={t('network.relayPaired.title')} desc={t('network.relayPaired.desc')}
                meta={t('network.shareOnly')} checked={settings.p2p_relay_paired} disabled={busy}
                onChange={(v) => onSave({ p2p_relay_paired: v })}
              />
              {settings.p2p_relay_paired && (
                <div className="p2ps-edit" data-field="p2p_relay_mode">
                  <label className="st-row__label" htmlFor="p2ps-relay-mode">{t('network.relayMode.title')}</label>
                  <p className="st-row__desc">{t('network.relayMode.desc')}</p>
                  <div className="p2ps-edit__row">
                    <div className="dk-select-wrap">
                      <select id="p2ps-relay-mode" className="dk-select" value={settings.p2p_relay_mode} disabled={busy} onChange={(e) => onSave({ p2p_relay_mode: e.target.value })}>
                        {RELAY_MODES.map(m => <option key={m} value={m}>{t(`network.relayMode.${m}`)}</option>)}
                      </select>
                    </div>
                    <span className="p2ps-key">p2p_relay_mode</span>
                  </div>
                </div>
              )}
              {settings.p2p_relay_paired && (
                <div className="p2ps-edit" data-field="p2p_relay_rate_limit">
                  <label className="st-row__label" htmlFor="p2ps-relay-rate">{t('network.relayRate.title')}</label>
                  <p className="st-row__desc">{t('network.relayRate.desc')}</p>
                  <div className="p2ps-edit__row">
                    <span className="p2ps-unit">
                      <input id="p2ps-relay-rate" className="dk-input p2ps-num" inputMode="numeric" value={rate} aria-invalid={!rateValid || undefined} onChange={(e) => setRate(e.target.value)} />
                      <span className="p2ps-unit__label">{t('units.kibps')}</span>
                    </span>
                    <Button size="sm" disabled={!rateChanged || busy} busy={saving === 'p2p_relay_rate_limit'} onClick={() => onSave({ p2p_relay_rate_limit: Number(rate) * 1024 })} data-testid="p2p-relay-rate-save">
                      {t('actions.save')}
                    </Button>
                    <span className="p2ps-key">p2p_relay_rate_limit</span>
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      )}
      {mode !== 'share' && <Callout testId="p2p-share-only-note">{t('network.shareOnlyNote')}</Callout>}
    </section>
  )
}
