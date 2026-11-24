// eslint-disable-next-line no-unused-vars
import Dialog from './Dialog'

// The add-on SDK's Modal (sdk.ui.Modal): a dialog with no head, for content
// that brings its own heading and actions. It draws the shared Dialog, so it
// has the same surface, motion and focus handling as every other dialog. Core
// pages use Dialog, ConfirmDialog or SideSheet; this stays for add-ons.
//
//   onClose     Escape, a click on the veil
//   ariaLabel   the accessible name of the dialog
//   maxWidth    kept for add-ons: above 600px the dialog takes the wide width
export default function Modal({ onClose, children, maxWidth = '600px', ariaLabel }) {
  const wide = parseInt(String(maxWidth), 10) > 600
  return (
    <Dialog title={null} closable={false} ariaLabel={ariaLabel} wide={wide} onClose={() => onClose?.()}>
      {children}
    </Dialog>
  )
}
