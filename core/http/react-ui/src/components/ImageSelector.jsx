import { useState } from 'react'

const GPU_OPTIONS = [
  { key: 'cpu',      label: 'CPU',             icon: 'fa-microchip', tag: 'latest-cpu',                  devTag: 'master-cpu',                  dockerFlags: '' },
  { key: 'cuda12',   label: 'CUDA 12',         icon: 'fa-bolt',      tag: 'latest-gpu-nvidia-cuda-12',   devTag: 'master-gpu-nvidia-cuda-12',   dockerFlags: '--gpus all' },
  { key: 'cuda13',   label: 'CUDA 13',         icon: 'fa-bolt',      tag: 'latest-gpu-nvidia-cuda-13',   devTag: 'master-gpu-nvidia-cuda-13',   dockerFlags: '--gpus all' },
  { key: 'l4t12',    label: 'L4T CUDA 12',     icon: 'fa-bolt',      tag: 'latest-gpu-nvidia-l4t-cuda12',devTag: 'master-gpu-nvidia-l4t-cuda12',dockerFlags: '--runtime nvidia' },
  { key: 'l4t13',    label: 'L4T CUDA 13',     icon: 'fa-bolt',      tag: 'latest-gpu-nvidia-l4t-cuda13',devTag: 'master-gpu-nvidia-l4t-cuda13',dockerFlags: '--runtime nvidia' },
  { key: 'amd',      label: 'AMD',             icon: 'fa-fire',      tag: 'latest-gpu-hipblas',           devTag: 'master-gpu-hipblas',           dockerFlags: '--device /dev/kfd --device /dev/dri' },
  { key: 'intel',    label: 'Intel',           icon: 'fa-atom',      tag: 'latest-gpu-intel',             devTag: 'master-gpu-intel',             dockerFlags: '--device /dev/dri' },
  { key: 'vulkan',   label: 'Vulkan',          icon: 'fa-globe',     tag: 'latest-gpu-vulkan',            devTag: 'master-gpu-vulkan',            dockerFlags: '--device /dev/dri' },
]

export function useImageSelector(defaultKey = 'cpu') {
  const [selected, setSelected] = useState(defaultKey)
  const [dev, setDev] = useState(false)
  const option = GPU_OPTIONS.find(o => o.key === selected) || GPU_OPTIONS[0]
  return { selected, setSelected, option, options: GPU_OPTIONS, dev, setDev }
}

export default function ImageSelector({ selected, onSelect, dev, onDevChange }) {
  return (
    <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap', marginBottom: 'var(--spacing-md)', alignItems: 'center' }}>
      {GPU_OPTIONS.map(opt => {
        const active = selected === opt.key
        return (
          <button
            key={opt.key}
            onClick={() => onSelect(opt.key)}
            style={{
              display: 'flex', alignItems: 'center', gap: 6,
              padding: '6px 12px',
              borderRadius: 'var(--radius-sm)',
              border: active ? '1px solid var(--color-primary)' : '1px solid var(--color-border-subtle)',
              background: active ? 'var(--color-primary-light)' : 'var(--color-bg-primary)',
              color: active ? 'var(--color-primary)' : 'var(--color-text-secondary)',
              cursor: 'pointer',
              fontSize: '0.8125rem',
              fontWeight: active ? 600 : 400,
              transition: 'all 150ms',
            }}
          >
            <i className={`fas ${opt.icon}`} style={{ fontSize: '0.75rem' }} />
            {opt.label}
          </button>
        )
      })}
      {onDevChange && (
        <button
          onClick={() => onDevChange(!dev)}
          style={{
            display: 'flex', alignItems: 'center', gap: 6,
            padding: '6px 12px',
            borderRadius: 'var(--radius-sm)',
            border: dev ? '1px solid var(--color-warning)' : '1px solid var(--color-border-subtle)',
            background: dev ? 'var(--color-warning-light)' : 'var(--color-bg-primary)',
            color: dev ? 'var(--color-warning)' : 'var(--color-text-muted)',
            cursor: 'pointer',
            fontSize: '0.8125rem',
            fontWeight: dev ? 600 : 400,
            transition: 'all 150ms',
          }}
          title="Use development (master) images instead of stable releases"
        >
          <i className="fas fa-flask" style={{ fontSize: '0.75rem' }} />
          Dev
        </button>
      )}
    </div>
  )
}

// Helper to build a docker image string
export function dockerImage(option, dev = false) {
  return `localai/localai:${dev ? option.devTag : option.tag}`
}

// Helper to build docker run flags (--gpus all, --device, etc.)
export function dockerFlags(option) {
  return option.dockerFlags
}
