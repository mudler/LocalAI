// Decorative line icons always accompany text; they never change accessible names.
const paths = {
  source: 'M5 4h14M12 4v16M8 20h8',
  review: 'M4 4h16v12H9l-5 4V4M8 8h8M8 12h5',
  image: 'M3 3h18v18H3zM3 16l6-6 5 5 3-3 4 4M16 7h.01',
  extract: 'M8 3H3v5M16 3h5v5M21 16v5h-5M8 21H3v-5M8 8h8v8H8z',
  mesh: 'M12 2l9 5v10l-9 5-9-5V7zM3 7l9 5 9-5M12 12v10',
  remesh: 'M3 3h18v18H3zM3 3l18 18M21 3L3 21M12 3v18M3 12h18',
  rig: 'M10 3h4v4h-4zM12 7v7M5 9l7 3 7-3M12 14l-5 7M12 14l5 7',
  skin: 'M4 4h16v16H4zM8 8h8v8H8zM4 4l4 4M20 4l-4 4M20 20l-4-4M4 20l4-4',
  motion: 'M13 3h4v4h-4zM14 8l-4 6 5 3-2 5M10 14l-5 6M10 10l-5 1M14 8l4 5h4',
  retarget: 'M3 6h12M11 2l4 4-4 4M21 18H9M13 14l-4 4 4 4M3 15v6h3M21 3v6h-3',
  run: 'M7 4l14 8-14 8z',
  pause: 'M7 4h3v16H7zM14 4h3v16h-3z',
  step: 'M4 4l12 8-12 8zM20 4v16',
  retry: 'M3 10a9 9 0 1 1 2 8M3 3v7h7',
  rewind: 'M11 5l-8 7 8 7zM21 5l-8 7 8 7z',
  history: 'M3 11a9 9 0 1 1 2 7M3 4v7h7M12 7v5l4 2',
  nodes: 'M3 3h6v6H3zM15 15h6v6h-6zM6 9v9h9',
  status: 'M5 3h14v18H5zM8 7h8M8 12h8M8 17h5',
  download: 'M12 3v12M7 10l5 5 5-5M4 16v5h16v-5',
  preview: 'M2 12s4-7 10-7 10 7 10 7-4 7-10 7S2 12 2 12M10 10h4v4h-4z',
  close: 'M6 6l12 12M18 6L6 18',
  add: 'M12 4v16M4 12h16',
  check: 'M4 12l5 5L20 6',
  edit: 'M4 16L16 4l4 4L8 20H4zM13 7l4 4',
  down: 'M12 3v18M5 14l7 7 7-7',
  save: 'M4 3h13l4 4v14H3V3zM7 3v6h9V3M7 21v-8h10v8',
}

export function createIcon(React) {
  return function Icon({ name }) {
    return React.createElement('svg', { className: 'ap-icon', viewBox: '0 0 24 24', 'aria-hidden': true, focusable: 'false' },
      React.createElement('path', { d: paths[name] || paths.status }))
  }
}
