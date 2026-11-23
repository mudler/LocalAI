/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useId } from 'react'
import Dialog from '../Dialog'

// The modal of the host access screens: the shared Dialog with a body that
// scrolls and the actions in the foot. Escape closes it unless `busy`; Tab
// stays inside; focus goes to the control marked data-autofocus (or the first
// field) and comes back to the opener.
//
// `dismissWhenBusy` is for a dialog whose work goes on in the background: it
// closes at any moment (Escape, the veil, the close button) and closing never
// stops that work. `closeLabel` adds the close button to the head.
export default function HostDialog({ title, children, actions, onClose, busy = false, dismissWhenBusy = false, closeLabel, testId }) {
  const titleId = useId()
  const locked = busy && !dismissWhenBusy
  return (
    <Dialog
      title={title}
      labelId={titleId}
      className="ad-dialog"
      bodyClass="ad-confirm"
      testId={testId}
      pending={locked}
      closable={Boolean(closeLabel)}
      closeLabel={closeLabel}
      closeTestId="dialog-close"
      onClose={onClose}
      foot={actions}
    >
      {children}
    </Dialog>
  )
}
