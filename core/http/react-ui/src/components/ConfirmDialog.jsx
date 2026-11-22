import { useId } from 'react'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars
import Dialog from './Dialog'
import Icon from './Icon'
// eslint-disable-next-line no-unused-vars
import { Button } from './ui'

// A confirmation: the shared Dialog as an alertdialog, with Cancel and one
// confirm button. Dialog does the portal, the focus trap, Escape and the veil;
// it is a modal layer, so a confirmation over a side sheet closes first.
//
//   open                 draws nothing while false
//   title, message       the head and the body (the body describes the dialog)
//   confirmLabel, cancelLabel, pendingLabel
//   danger               a destructive action: a warning icon and the danger button
//   pending              the action runs: nothing can dismiss the dialog
//   confirmDisabled      the confirm button is off (a typed check not met yet)
//   onConfirm, onCancel
//
// The first focus is on the confirm button; when it is off, on the first field
// of the message.
export default function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel,
  pendingLabel,
  cancelLabel,
  danger = false,
  pending = false,
  confirmDisabled = false,
  onConfirm,
  onCancel,
}) {
  const { t } = useTranslation('common')
  const id = useId()
  if (!open) return null

  const titleText = title ?? t('actions.confirm')
  const confirmText = confirmLabel ?? t('actions.confirm')
  const cancelText = cancelLabel ?? t('actions.cancel')
  const titleId = `${id}-title`
  const bodyId = `${id}-body`
  const confirmOff = pending || confirmDisabled

  return (
    <Dialog
      role="alertdialog"
      className="ui-confirm"
      labelId={titleId}
      bodyId={message ? bodyId : undefined}
      describedBy={message ? bodyId : undefined}
      title={danger ? <><Icon name="warning" className="ui-confirm__icon" />{titleText}</> : titleText}
      pending={pending}
      closable={false}
      onClose={() => onCancel?.()}
      foot={(
        <>
          <Button disabled={pending} onClick={onCancel}>{cancelText}</Button>
          <Button
            variant={danger ? 'danger' : 'primary'}
            disabled={confirmOff}
            data-autofocus={confirmOff ? undefined : true}
            onClick={onConfirm}
          >
            {pending ? (pendingLabel ?? confirmText) : confirmText}
          </Button>
        </>
      )}
    >
      {message || null}
    </Dialog>
  )
}
