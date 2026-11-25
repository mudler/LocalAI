// "Keep P2P connection in the background" in Settings > General (the Android
// app). The native side owns the value: the switch reads it, follows its
// changes (the notification's Disconnect turns it off) and shows what the
// native side answers after a tap, not what was asked.
import { useEffect, useId, useState } from 'react'
import { Callout, Switch } from '@localai/ui-kit'
import { useBridge } from '../bridge'
import { Card, Field, fieldIds } from '../parts'
import { S } from '../strings'

export function P2PKeepCard() {
  const b = useBridge()
  const id = useId()
  const [on, setOn] = useState<boolean>()
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    let live = true
    const off = b.onP2PKeepInBackground?.((v) => live && setOn(v))
    void b
      .getP2PKeepInBackground?.()
      .then((v) => live && setOn(v))
      .catch(() => undefined)
    return () => {
      live = false
      off?.()
    }
  }, [b])

  if (on === undefined || !b.setP2PKeepInBackground) return null

  const change = async (v: boolean) => {
    setBusy(true)
    setFailed(false)
    try {
      setOn(await b.setP2PKeepInBackground!(v))
    } catch {
      setFailed(true)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card title={S.androidSettings.p2pCard}>
      {failed && <Callout tone="error" role="alert" title={S.androidSettings.keepP2PFailed} />}
      <Field id={id} name={S.androidSettings.keepP2P} note={S.androidSettings.keepP2PNote}>
        <span className="lnr-grow" />
        <Switch checked={on} disabled={busy} onChange={(v: boolean) => void change(v)} {...fieldIds(id)} testId="keep-p2p" />
      </Field>
    </Card>
  )
}
