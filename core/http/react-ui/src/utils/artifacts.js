import { Marked } from 'marked'
import DOMPurify from 'dompurify'
import hljs from 'highlight.js'

const FENCE_REGEX = /```(\w*)\n([\s\S]*?)```/g

export function extractCodeArtifacts(messages, roleField = 'role', targetRole = 'assistant') {
  if (!messages) return []
  const artifacts = []
  messages.forEach((msg, mi) => {
    if (msg[roleField] !== targetRole) return
    const text = typeof msg.content === 'string' ? msg.content : ''
    if (!text) return
    let match
    let blockIndex = 0
    const re = new RegExp(FENCE_REGEX.source, 'g')
    while ((match = re.exec(text)) !== null) {
      const lang = (match[1] || 'text').toLowerCase()
      const code = match[2]
      artifacts.push({
        id: `${mi}-${blockIndex}`,
        type: 'code',
        language: lang,
        code,
        title: guessTitle(lang, blockIndex),
        messageIndex: mi,
      })
      blockIndex++
    }
  })
  return artifacts
}

const IMAGE_EXTS = /\.(png|jpe?g|gif|webp|bmp|svg|ico)$/i
const AUDIO_EXTS = /\.(mp3|wav|ogg|flac|aac|m4a|wma)$/i
const VIDEO_EXTS = /\.(mp4|webm|mkv|avi|mov)$/i
const PDF_EXT = /\.pdf$/i

export function inferMetadataType(key, value) {
  const k = key.toLowerCase()
  if (k.includes('image') || k.includes('img') || k.includes('photo') || k.includes('picture')) return 'image'
  if (k.includes('pdf')) return 'pdf'
  if (k.includes('song') || k.includes('audio') || k.includes('music') || k.includes('voice') || k.includes('tts')) return 'audio'
  if (k.includes('video')) return 'video'
  if (k === 'urls' || k === 'url' || k.includes('links')) return 'url'
  // Infer from value content
  if (IMAGE_EXTS.test(value)) return 'image'
  if (AUDIO_EXTS.test(value)) return 'audio'
  if (VIDEO_EXTS.test(value)) return 'video'
  if (PDF_EXT.test(value)) return 'pdf'
  try { new URL(value); return 'url' } catch (_e) { /* not a URL */ }
  return 'file'
}

function isWebUrl(v) {
  return typeof v === 'string' && (v.startsWith('http://') || v.startsWith('https://'))
}

export function extractMetadataArtifacts(messages, agentName) {
  if (!messages) return []
  const artifacts = []
  messages.forEach((msg, mi) => {
    const meta = msg.metadata
    if (!meta) return
    const fileUrl = (absPath) => {
      if (!agentName) return absPath
      return `/api/agents/${encodeURIComponent(agentName)}/files?path=${encodeURIComponent(absPath)}`
    }
    Object.entries(meta).forEach(([key, values]) => {
      if (!Array.isArray(values)) return
      values.forEach((v, i) => {
        if (typeof v !== 'string') return
        const type = inferMetadataType(key, v)
        const url = isWebUrl(v) ? v : fileUrl(v)
        let title
        if (type === 'url') {
          try { title = new URL(v).hostname } catch (_e) { title = v }
        } else {
          title = v.split('/').pop() || key
        }
        artifacts.push({ id: `meta-${mi}-${key}-${i}`, type, url, title, messageIndex: mi })
      })
    })
  })
  return artifacts
}

function guessTitle(lang, index) {
  const extMap = {
    html: 'index.html', javascript: 'script.js', js: 'script.js',
    typescript: 'script.ts', ts: 'script.ts', jsx: 'component.jsx', tsx: 'component.tsx',
    python: 'script.py', py: 'script.py', css: 'styles.css', svg: 'image.svg',
    json: 'data.json', yaml: 'config.yaml', yml: 'config.yaml',
    go: 'main.go', rust: 'main.rs', java: 'Main.java',
    markdown: 'document.md', md: 'document.md',
    bash: 'script.sh', sh: 'script.sh', sql: 'query.sql',
  }
  const base = extMap[lang] || `snippet-${index}.${lang || 'txt'}`
  return index > 0 && extMap[lang] ? base.replace('.', `-${index}.`) : base
}

export function getArtifactIcon(type, language) {
  if (type === 'image') return 'fa-image'
  if (type === 'pdf') return 'fa-file-pdf'
  if (type === 'audio') return 'fa-music'
  if (type === 'video') return 'fa-video'
  if (type === 'url') return 'fa-link'
  if (type === 'file') return 'fa-file'
  if (type === 'code') {
    if (language === 'html') return 'fa-globe'
    if (language === 'svg') return 'fa-image'
    if (language === 'css') return 'fa-palette'
    if (language === 'md' || language === 'markdown') return 'fa-file-lines'
  }
  return 'fa-code'
}

const artifactMarked = new Marked({
  renderer: {
    code({ text, lang }) {
      // Will be overridden per-call
      if (lang && hljs.getLanguage(lang)) {
        const highlighted = hljs.highlight(text, { language: lang }).value
        return `<pre><code class="hljs language-${lang}">${highlighted}</code></pre>`
      }
      return `<pre><code>${text.replace(/</g, '&lt;').replace(/>/g, '&gt;')}</code></pre>`
    },
  },
  breaks: true,
  gfm: true,
})

export function renderMarkdownWithArtifacts(text, messageIndex) {
  if (!text) return ''

  // Check if there are any complete code blocks
  const hasComplete = /```\w*\n[\s\S]*?```/.test(text)
  if (!hasComplete) {
    // Fall back to normal rendering for incomplete/streaming content
    return DOMPurify.sanitize(artifactMarked.parse(text))
  }

  let blockIndex = 0
  const renderer = {
    code({ text: codeText, lang }) {
      const id = `${messageIndex}-${blockIndex}`
      const language = (lang || 'text').toLowerCase()
      const icon = getArtifactIcon('code', language)
      const title = guessTitle(language, blockIndex)
      blockIndex++
      return `<div class="artifact-card" data-artifact-id="${id}">
        <div class="artifact-card-icon"><i class="fas ${icon}"></i></div>
        <div class="artifact-card-info">
          <span class="artifact-card-title">${title}</span>
          <span class="artifact-card-lang">${language}</span>
        </div>
        <div class="artifact-card-actions">
          <button class="artifact-card-download" data-artifact-id="${id}" title="Download"><i class="fas fa-download"></i></button>
          <button class="artifact-card-open" data-artifact-id="${id}" title="Open in canvas"><i class="fas fa-external-link-alt"></i></button>
        </div>
      </div>`
    },
  }

  const customMarked = new Marked({ renderer, breaks: true, gfm: true })
  const html = customMarked.parse(text)
  return DOMPurify.sanitize(html, { ADD_ATTR: ['data-artifact-id'] })
}
