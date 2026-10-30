/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { filesApi } from '../../utils/api'
import { fileIcon, fileSize } from '../../utils/fileInfo'
import { attachmentLayout } from '../../utils/chatAttachments'
import useObjectUrl from '../../hooks/useObjectUrl'
import { openChatFile } from './chatActions'
import './attachments.css'

// One file as a chip: the type's icon (or a small thumbnail), the name, the
// size and, when the file can be fetched, a download link. The name opens the
// file when onOpen is given. The composer uses the same chip for the files
// it is about to send, with a remove button in place of the download.
//
//   name, size, mime   what the chip says
//   thumb              an image address for the 20 px thumbnail, or none
//   href               the download address, or none
//   onOpen             opens the file (the chat's files pane), or none
//   missing            the file is gone: a muted chip with no link
//   onRemove, removeLabel   the composer's remove button
export function AttachmentChip({
  name, size, mime = '', thumb, href, onOpen, missing = false, onRemove, removeLabel, openLabel, downloadLabel, missingLabel,
  testId = 'attachment',
}) {
  const body = (
    <>
      {thumb
        ? <img className="cx-att__thumb" src={thumb} alt="" />
        : <Icon name={fileIcon(name, mime)} className="cx-att__icon" />}
      <span className="cx-att__name">{name}</span>
      {missing
        ? <span className="cx-att__meta">{missingLabel}</span>
        : size > 0 && <span className="cx-att__meta">{fileSize(size)}</span>}
    </>
  )
  return (
    <span className={`cx-att${missing ? ' cx-att--missing' : ''}`} data-testid={testId} data-missing={missing || undefined}>
      {onOpen && !missing ? (
        <button type="button" className="cx-att__open" onClick={onOpen} aria-label={openLabel} title={name}>{body}</button>
      ) : (
        <span className="cx-att__open" title={name}>{body}</span>
      )}
      {href && !missing && (
        <a className="cx-att__act" href={href} download={name} aria-label={downloadLabel} title={downloadLabel}>
          <Icon name="download" />
        </a>
      )}
      {onRemove && (
        <button type="button" className="cx-att__act" onClick={onRemove} aria-label={removeLabel} title={removeLabel}>
          <Icon name="close" />
        </button>
      )}
    </span>
  )
}

// A file the composer holds before it is sent. An image shows a thumbnail
// read from the browser, never uploaded first.
export function StagedAttachment({ file, onRemove, removeLabel }) {
  const image = (file.type || '').startsWith('image/')
  const local = useObjectUrl(image && !file.base64 && typeof Blob !== 'undefined' && file instanceof Blob ? file : null)
  const thumb = image ? (file.base64 ? `data:${file.type};base64,${file.base64}` : local) : null
  return (
    <AttachmentChip
      name={file.name} size={file.size} mime={file.type} thumb={thumb}
      onRemove={onRemove} removeLabel={removeLabel}
    />
  )
}

// An image of a message. An image that does not load (deleted since the
// transcript was read) becomes the muted chip, never a broken image.
function ImageTile({ a, t }) {
  const [broken, setBroken] = useState(false)
  if (broken) {
    return <AttachmentChip name={a.name} mime={a.mime} missing missingLabel={t('attachments.missing')} />
  }
  return (
    <button
      type="button" className="cx-atts__tile" data-testid="attachment-image"
      onClick={() => openChatFile(a.path)} aria-label={t('attachments.open', { name: a.name })} title={a.name}
    >
      <img src={filesApi.contentUrl(a.path, true)} alt={a.name} loading="lazy" onError={() => setBroken(true)} />
    </button>
  )
}

// The files of a message of the person: images as a compact grid of
// thumbnails (at most four tiles, the last one counting the rest), any other
// file as a chip. A click or Enter opens the file in the chat's files pane;
// the chip also has a download link. A file that is gone is a muted chip.
export default function MessageAttachments({ attachments }) {
  const { t } = useTranslation('chat')
  const [all, setAll] = useState(false)
  if (!attachments?.length) return null
  const { images, more, files } = attachmentLayout(attachments)
  const shown = all ? [...images, ...more] : images
  return (
    <div className="cx-atts" data-testid="message-attachments" aria-label={t('attachments.label')} role="group">
      {shown.length > 0 && (
        <div className={`cx-atts__grid${shown.length === 1 ? ' cx-atts__grid--one' : ''}`}>
          {shown.map((a) => <ImageTile key={a.path} a={a} t={t} />)}
          {!all && more.length > 0 && (
            <button
              type="button" className="cx-atts__tile cx-atts__more" data-testid="attachment-more"
              onClick={() => setAll(true)} aria-label={t('attachments.showMore', { count: more.length })}
            >
              +{more.length}
            </button>
          )}
        </div>
      )}
      {files.map((a) => (
        <AttachmentChip
          key={a.path || a.name} name={a.name} size={a.size} mime={a.mime} missing={a.missing}
          href={a.missing ? undefined : filesApi.contentUrl(a.path)}
          onOpen={() => openChatFile(a.path)}
          openLabel={t('attachments.open', { name: a.name })}
          downloadLabel={t('files.download', { name: a.name })}
          missingLabel={t('attachments.missing')}
        />
      ))}
    </div>
  )
}
