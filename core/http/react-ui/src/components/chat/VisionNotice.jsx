import { useTranslation } from 'react-i18next'
import { visionNotice } from '../../utils/chatVision'

// Says, as soon as an image is attached, that the chat's model cannot look at
// it and what happens instead. Nothing when the model can.
export default function VisionNotice({ files, model, models, imageModel }) {
  const { t } = useTranslation('chats')
  const n = visionNotice({ files, model, models, imageModel })
  if (!n) return null
  return (
    <p className="cx-notice" role="note" data-testid="chat-vision-notice">
      {t(`vision.${n.key}`, { model: n.model })}
    </p>
  )
}
