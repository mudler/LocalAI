import { useState } from 'react'

// The hardware choices for a Docker image: the tag, the development tag and the
// flags that give the container the device.
const GPU_OPTIONS = [
  { key: 'cpu',      label: 'CPU',             icon: 'cpu', tag: 'latest-cpu',                  devTag: 'master-cpu',                  dockerFlags: '' },
  { key: 'cuda12',   label: 'CUDA 12',         icon: 'bolt',      tag: 'latest-gpu-nvidia-cuda-12',   devTag: 'master-gpu-nvidia-cuda-12',   dockerFlags: '--gpus all' },
  { key: 'cuda13',   label: 'CUDA 13',         icon: 'bolt',      tag: 'latest-gpu-nvidia-cuda-13',   devTag: 'master-gpu-nvidia-cuda-13',   dockerFlags: '--gpus all' },
  { key: 'l4t12',    label: 'L4T CUDA 12',     icon: 'bolt',      tag: 'latest-gpu-nvidia-l4t-cuda12',devTag: 'master-gpu-nvidia-l4t-cuda12',dockerFlags: '--runtime nvidia' },
  { key: 'l4t13',    label: 'L4T CUDA 13',     icon: 'bolt',      tag: 'latest-gpu-nvidia-l4t-cuda13',devTag: 'master-gpu-nvidia-l4t-cuda13',dockerFlags: '--runtime nvidia' },
  { key: 'amd',      label: 'AMD',             icon: 'flame',      tag: 'latest-gpu-hipblas',           devTag: 'master-gpu-hipblas',           dockerFlags: '--device /dev/kfd --device /dev/dri' },
  { key: 'intel',    label: 'Intel',           icon: 'atom',      tag: 'latest-gpu-intel',             devTag: 'master-gpu-intel',             dockerFlags: '--device /dev/dri' },
  { key: 'vulkan',   label: 'Vulkan',          icon: 'globe',     tag: 'latest-gpu-vulkan',            devTag: 'master-gpu-vulkan',            dockerFlags: '--device /dev/dri' },
]

export function useImageSelector(defaultKey = 'cpu') {
  const [selected, setSelected] = useState(defaultKey)
  const [dev, setDev] = useState(false)
  const option = GPU_OPTIONS.find(o => o.key === selected) || GPU_OPTIONS[0]
  return { selected, setSelected, option, options: GPU_OPTIONS, dev, setDev }
}
