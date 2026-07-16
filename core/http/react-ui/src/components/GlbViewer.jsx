import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { parseGlb } from '../utils/glb'

/* ── WebGL2 GLB viewer ──────────────────────────────────────────────────────
 * Ported from the trellis2cpp demo server's hand-rolled viewer
 * (localai-org/trellis2cpp server/web/index.html): quaternion trackball,
 * metallic-roughness PBR with a procedural environment, ACES tonemapping,
 * hidden-line wireframe. Kept dependency-free — the renderer is tuned for
 * the dense unoriented dual-grid meshes TRELLIS.2 produces, which generic
 * GLTF viewers shade poorly.
 *
 * Deltas from the demo: input is a parsed GLB (see utils/glb.js) instead of
 * the demo's T2MESH stream; COLOR_0 arrives LINEAR (no gamma decode — the
 * demo's pow(2.2) would double-darken it); vertex attributes upload as
 * normalized integers straight from the GLB buffers; an optional UV-texture
 * path covers the atlas-baked form; the base orientation drops the demo's
 * Z-up -> Y-up turn because baked GLBs are already Y-up.
 */

const VS = `#version 300 es
layout(location=0) in vec3 pos;
layout(location=1) in vec3 nrm;
layout(location=2) in vec4 baseColor;
layout(location=3) in vec2 metalRough;
layout(location=4) in vec2 uv;
uniform mat4 mvp, viewModel;
out vec3 vN; out vec3 vV; out vec4 vCol; out vec2 vMR; out vec2 vUV;
void main() {
  gl_Position = mvp * vec4(pos, 1.0);
  vec4 vp = viewModel * vec4(pos, 1.0);
  vV  = -vp.xyz;                    // view-space: to-camera (camera at origin)
  vN  = mat3(viewModel) * nrm;      // view-space normal (orbits with camera)
  vCol = baseColor;
  vMR = metalRough;
  vUV = uv;
}`

// Lightweight metallic-roughness PBR in view space. The dual-grid mesh has
// unoriented winding (faithful to TRELLIS.2), so every normal is faced toward
// the camera. No IBL — a procedural environment + one studio key light;
// ACES tonemapping at the end.
const FS = `#version 300 es
precision highp float;
in vec3 vN; in vec3 vV; in vec4 vCol; in vec2 vMR; in vec2 vUV;
uniform int wire;
uniform int colorMode;  // 0 = untextured grey, 1 = vertex PBR (linear COLOR_0), 2 = UV textures
uniform sampler2D baseColorTex;
uniform sampler2D mrTex;
uniform float uMetallicFactor, uRoughnessFactor;
out vec4 frag;
// procedural environment radiance in a direction (view space; +y = up)
vec3 envColor(vec3 d) {
  vec3 sky = mix(vec3(0.32, 0.40, 0.55), vec3(0.72, 0.80, 0.98), clamp(d.y, 0.0, 1.0));
  vec3 gnd = vec3(0.14, 0.13, 0.12);
  return mix(gnd, sky, smoothstep(-0.30, 0.12, d.y));
}
void main() {
  if (wire == 1) { frag = vec4(0.30, 0.64, 1.00, 1.0); return; }
  vec3 N = normalize(vN), V = normalize(vV);
  if (dot(N, V) < 0.0) N = -N;   // face the camera (mesh is unoriented)
  vec3 base = vec3(0.62, 0.66, 0.72);
  float metal = 0.0, rough = 0.5, opacity = 1.0;
  if (colorMode == 1) {
    // COLOR_0 is stored linear in the GLB — use it directly.
    base = clamp(vCol.rgb, 0.0, 1.0);
    metal = clamp(vMR.x, 0.0, 1.0);
    rough = clamp(vMR.y, 0.06, 1.0);
    opacity = clamp(vCol.a, 0.0, 1.0);
  } else if (colorMode == 2) {
    vec4 bc = texture(baseColorTex, vUV);
    base = pow(clamp(bc.rgb, 0.0, 1.0), vec3(2.2));  // baseColorTexture is sRGB
    opacity = bc.a;
    vec3 mr = texture(mrTex, vUV).rgb;               // G=roughness, B=metallic
    metal = clamp(mr.b * uMetallicFactor, 0.0, 1.0);
    rough = clamp(mr.g * uRoughnessFactor, 0.06, 1.0);
  }
  if (opacity < 0.01) discard;
  float nv = max(dot(N, V), 1e-3);

  // Schlick Fresnel (grazing reflectance rises to 1 - roughness for metals).
  vec3 F0 = mix(vec3(0.04), base, metal);
  vec3 F  = F0 + (max(vec3(1.0 - rough), F0) - F0) * pow(1.0 - nv, 5.0);

  // diffuse: hemispheric environment irradiance (metals have no diffuse)
  vec3 irr = envColor(N) * 0.55 + vec3(0.12);
  vec3 diffuse = base * (1.0 - metal) * irr;

  // specular: environment reflection, blurred toward a flat tint by roughness
  vec3 refl = mix(envColor(reflect(-V, N)), vec3(0.34, 0.37, 0.44), rough * rough);
  vec3 specular = refl * F;

  // one crisp studio key light for a lively highlight
  vec3 L = normalize(vec3(0.45, 0.70, 0.55)), H = normalize(L + V);
  float shin = mix(8.0, 260.0, pow(1.0 - rough, 2.0));
  float sp = pow(max(dot(N, H), 0.0), shin) * (shin + 2.0) / 6.2831853;
  vec3  kc = vec3(1.00, 0.96, 0.88);
  float ndl = max(dot(N, L), 0.0);
  specular += kc * sp * F * ndl;
  diffuse  += base * (1.0 - metal) * kc * ndl * 0.28;

  vec3 color = diffuse + specular;
  color = (color * (2.51 * color + 0.03)) / (color * (2.43 * color + 0.59) + 0.14); // ACES
  frag = vec4(pow(clamp(color, 0.0, 1.0), vec3(1.0/2.2)), opacity);
}`

/* trackball orientation (quaternion): each drag composes a small rotation
 * about the screen axes onto the current orientation — no gimbal lock. */
const Q = {
  axisAngle(x, y, z, a) { const h = a * 0.5, s = Math.sin(h); return [x * s, y * s, z * s, Math.cos(h)] },
  mul(a, b) {   // Hamilton product a·b (apply b, then a)
    return [
      a[3] * b[0] + a[0] * b[3] + a[1] * b[2] - a[2] * b[1],
      a[3] * b[1] - a[0] * b[2] + a[1] * b[3] + a[2] * b[0],
      a[3] * b[2] + a[0] * b[1] - a[1] * b[0] + a[2] * b[3],
      a[3] * b[3] - a[0] * b[0] - a[1] * b[1] - a[2] * b[2],
    ]
  },
  norm(q) { const n = Math.hypot(q[0], q[1], q[2], q[3]) || 1; return [q[0] / n, q[1] / n, q[2] / n, q[3] / n] },
  toMat4(q) {   // column-major rotation matrix
    const [x, y, z, w] = q
    const xx = x * x, yy = y * y, zz = z * z, xy = x * y, xz = x * z, yz = y * z, wx = w * x, wy = w * y, wz = w * z
    return new Float32Array([
      1 - 2 * (yy + zz), 2 * (xy + wz), 2 * (xz - wy), 0,
      2 * (xy - wz), 1 - 2 * (xx + zz), 2 * (yz + wx), 0,
      2 * (xz + wy), 2 * (yz - wx), 1 - 2 * (xx + yy), 0,
      0, 0, 0, 1,
    ])
  },
}

// GLBs are already Y-up (the baker swaps axes on export), so unlike the demo
// there is no Z-up correction here — just a gentle 3/4 default view.
const QBASE = Q.norm(Q.mul(Q.axisAngle(1, 0, 0, -0.30), Q.axisAngle(0, 1, 0, 0.55)))

/* minimal mat4 helpers (column-major) */
const M = {
  mul(a, b) {
    const o = new Float32Array(16)
    for (let c = 0; c < 4; ++c) for (let r = 0; r < 4; ++r)
      o[c * 4 + r] = a[r] * b[c * 4] + a[4 + r] * b[c * 4 + 1] + a[8 + r] * b[c * 4 + 2] + a[12 + r] * b[c * 4 + 3]
    return o
  },
  persp(fov, asp, near, far) {
    const f = 1 / Math.tan(fov / 2), o = new Float32Array(16)
    o[0] = f / asp; o[5] = f
    o[10] = (far + near) / (near - far); o[11] = -1
    o[14] = 2 * far * near / (near - far)
    return o
  },
  trans(x, y, z) {
    const o = new Float32Array(16)
    o[0] = o[5] = o[10] = o[15] = 1
    o[12] = x; o[13] = y; o[14] = z
    return o
  },
  scale(s) {
    const o = new Float32Array(16)
    o[0] = o[5] = o[10] = s; o[15] = 1
    return o
  },
}

function glType(gl, array) {
  if (array instanceof Uint8Array) return gl.UNSIGNED_BYTE
  if (array instanceof Uint16Array) return gl.UNSIGNED_SHORT
  return gl.FLOAT
}

export function createGlbViewer(canvas, { onContextLost } = {}) {
  const gl = canvas.getContext('webgl2', { antialias: true })
  if (!gl) return null

  // If the GPU context is lost (driver reset / out of memory), preventDefault
  // keeps it recoverable and the page shows a notice instead of a dead canvas.
  const contextLost = (e) => {
    e.preventDefault()
    if (onContextLost) onContextLost()
  }
  canvas.addEventListener('webglcontextlost', contextLost, false)

  function shader(type, src) {
    const s = gl.createShader(type)
    gl.shaderSource(s, src); gl.compileShader(s)
    if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(s))
    return s
  }
  const prog = gl.createProgram()
  gl.attachShader(prog, shader(gl.VERTEX_SHADER, VS))
  gl.attachShader(prog, shader(gl.FRAGMENT_SHADER, FS))
  gl.linkProgram(prog)
  if (!gl.getProgramParameter(prog, gl.LINK_STATUS)) throw new Error(gl.getProgramInfoLog(prog))
  const uMVP = gl.getUniformLocation(prog, 'mvp')
  const uViewModel = gl.getUniformLocation(prog, 'viewModel')
  const uWire = gl.getUniformLocation(prog, 'wire')
  const uColorMode = gl.getUniformLocation(prog, 'colorMode')
  const uBaseColorTex = gl.getUniformLocation(prog, 'baseColorTex')
  const uMrTex = gl.getUniformLocation(prog, 'mrTex')
  const uMetallicFactor = gl.getUniformLocation(prog, 'uMetallicFactor')
  const uRoughnessFactor = gl.getUniformLocation(prog, 'uRoughnessFactor')

  const vao = gl.createVertexArray()
  const vbo = gl.createBuffer(), nbo = gl.createBuffer(), cbo = gl.createBuffer()
  const mbo = gl.createBuffer(), ubo = gl.createBuffer(), ibo = gl.createBuffer()
  const wireIbo = gl.createBuffer()
  let nIndices = 0, nWire = 0
  let colorMode = 0
  let baseColorTexture = null, mrTexture = null
  let metallicFactor = 1, roughnessFactor = 1
  // fit-to-view: model = rotation * scale * translate(-center) keeps the demo's
  // camera constants valid for any GLB extent (trellis meshes are ~unit cube).
  let center = [0, 0, 0], fitScale = 1

  let rot = QBASE.slice(), dist = 1.8, panX = 0, panY = 0
  let wire = false, spin = true
  let disposed = false

  function makeTexture(bitmap) {
    const tex = gl.createTexture()
    gl.bindTexture(gl.TEXTURE_2D, tex)
    // Matches the baker's sampler: linear filtering, clamp, no mipmaps.
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
    gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
    gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, bitmap)
    gl.bindTexture(gl.TEXTURE_2D, null)
    return tex
  }

  function dropTextures() {
    if (baseColorTexture) { gl.deleteTexture(baseColorTexture); baseColorTexture = null }
    if (mrTexture) { gl.deleteTexture(mrTexture); mrTexture = null }
  }

  async function setMesh(mesh) {
    gl.bindVertexArray(vao)
    gl.bindBuffer(gl.ARRAY_BUFFER, vbo)
    gl.bufferData(gl.ARRAY_BUFFER, mesh.positions, gl.STATIC_DRAW)
    gl.enableVertexAttribArray(0)
    gl.vertexAttribPointer(0, 3, gl.FLOAT, false, 0, 0)
    if (mesh.normals) {
      gl.bindBuffer(gl.ARRAY_BUFFER, nbo)
      gl.bufferData(gl.ARRAY_BUFFER, mesh.normals, gl.STATIC_DRAW)
      gl.enableVertexAttribArray(1)
      gl.vertexAttribPointer(1, 3, gl.FLOAT, false, 0, 0)
    } else {
      gl.disableVertexAttribArray(1)
      gl.vertexAttrib3f(1, 0, 1, 0)
    }

    dropTextures()
    colorMode = 0
    if (mesh.color0) {
      // Upload COLOR_0 / _METALLIC_ROUGHNESS as normalized integers straight
      // from the GLB buffers — no CPU conversion of multi-million-vertex data.
      gl.bindBuffer(gl.ARRAY_BUFFER, cbo)
      gl.bufferData(gl.ARRAY_BUFFER, mesh.color0.array, gl.STATIC_DRAW)
      gl.enableVertexAttribArray(2)
      gl.vertexAttribPointer(2, mesh.color0.size, glType(gl, mesh.color0.array), mesh.color0.normalized, 0, 0)
      if (mesh.metalRough) {
        gl.bindBuffer(gl.ARRAY_BUFFER, mbo)
        gl.bufferData(gl.ARRAY_BUFFER, mesh.metalRough.array, gl.STATIC_DRAW)
        gl.enableVertexAttribArray(3)
        gl.vertexAttribPointer(3, mesh.metalRough.size, glType(gl, mesh.metalRough.array), mesh.metalRough.normalized, 0, 0)
      } else {
        gl.disableVertexAttribArray(3)
        gl.vertexAttrib2f(3, 0, 0.6)
      }
      gl.disableVertexAttribArray(4)
      colorMode = 1
    } else if (mesh.uv && mesh.baseColorPng) {
      gl.bindBuffer(gl.ARRAY_BUFFER, ubo)
      gl.bufferData(gl.ARRAY_BUFFER, mesh.uv, gl.STATIC_DRAW)
      gl.enableVertexAttribArray(4)
      gl.vertexAttribPointer(4, 2, gl.FLOAT, false, 0, 0)
      gl.disableVertexAttribArray(2)
      gl.disableVertexAttribArray(3)
      const bitmaps = await Promise.all([
        createImageBitmap(new Blob([mesh.baseColorPng], { type: 'image/png' })),
        mesh.metalRoughPng ? createImageBitmap(new Blob([mesh.metalRoughPng], { type: 'image/png' })) : null,
      ])
      if (disposed) return
      gl.bindVertexArray(vao)
      baseColorTexture = makeTexture(bitmaps[0])
      mrTexture = bitmaps[1] ? makeTexture(bitmaps[1]) : makeTexture(bitmaps[0])
      metallicFactor = mesh.material.metallicFactor
      roughnessFactor = mesh.material.roughnessFactor
      colorMode = 2
    } else {
      gl.disableVertexAttribArray(2)
      gl.disableVertexAttribArray(3)
      gl.disableVertexAttribArray(4)
    }

    gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, ibo)
    gl.bufferData(gl.ELEMENT_ARRAY_BUFFER, mesh.indices, gl.STATIC_DRAW)
    nIndices = mesh.indices.length
    // wireframe index buffer: 3 edges per triangle, bounded to WIRE_BUDGET —
    // a full wireframe of a multi-million-triangle mesh would OOM the GPU and
    // lose the context (sub-pixel lines also fill in as a solid mass).
    const WIRE_BUDGET = 12_000_000               // ~6M segments, ~48 MB
    const nTri = nIndices / 3
    const wireStride = Math.max(1, Math.ceil(nTri * 6 / WIRE_BUDGET))
    const wireIdx = new Uint32Array(Math.ceil(nTri / wireStride) * 6)
    let o = 0
    const idx = mesh.indices
    for (let t = 0; t < nIndices; t += 3 * wireStride) {
      wireIdx[o++] = idx[t]; wireIdx[o++] = idx[t + 1]
      wireIdx[o++] = idx[t + 1]; wireIdx[o++] = idx[t + 2]
      wireIdx[o++] = idx[t + 2]; wireIdx[o++] = idx[t]
    }
    gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, wireIbo)
    gl.bufferData(gl.ELEMENT_ARRAY_BUFFER, wireIdx.subarray(0, o), gl.STATIC_DRAW)
    nWire = o
    gl.bindVertexArray(null)

    center = [
      (mesh.bboxMin[0] + mesh.bboxMax[0]) / 2,
      (mesh.bboxMin[1] + mesh.bboxMax[1]) / 2,
      (mesh.bboxMin[2] + mesh.bboxMax[2]) / 2,
    ]
    const radius = Math.hypot(
      mesh.bboxMax[0] - mesh.bboxMin[0],
      mesh.bboxMax[1] - mesh.bboxMin[1],
      mesh.bboxMax[2] - mesh.bboxMin[2],
    ) / 2 || 1
    fitScale = 0.866 / radius
    resetView()
  }

  function clear() {
    nIndices = 0
    nWire = 0
    dropTextures()
  }

  function resetView() {
    rot = QBASE.slice(); dist = 1.8; panX = panY = 0
  }

  /* input */
  let dragging = false, panning = false, lx = 0, ly = 0
  const onMouseDown = (e) => {
    dragging = true
    panning = e.button === 2 || e.shiftKey
    lx = e.clientX; ly = e.clientY
  }
  const onMouseUp = () => { dragging = false }
  const onMouseMove = (e) => {
    if (!dragging) return
    const dx = e.clientX - lx, dy = e.clientY - ly
    lx = e.clientX; ly = e.clientY
    if (panning) {
      panX += dx * 0.0015 * dist; panY -= dy * 0.0015 * dist
    } else {
      // Compose screen-axis turns onto the current orientation (fixed camera
      // frame), so rotation stays screen-relative and never locks up.
      const k = 0.008
      rot = Q.norm(Q.mul(Q.axisAngle(1, 0, 0, dy * k), Q.mul(Q.axisAngle(0, 1, 0, dx * k), rot)))
      spin = false
      if (onSpinChange) onSpinChange(false)
    }
  }
  const onContextMenu = (e) => e.preventDefault()
  const onWheel = (e) => {
    e.preventDefault()
    dist *= Math.exp(e.deltaY * 0.001)
    dist = Math.max(0.3, Math.min(8, dist))
  }
  const onDblClick = () => resetView()
  let onSpinChange = null

  canvas.addEventListener('mousedown', onMouseDown)
  window.addEventListener('mouseup', onMouseUp)
  window.addEventListener('mousemove', onMouseMove)
  canvas.addEventListener('contextmenu', onContextMenu)
  canvas.addEventListener('wheel', onWheel, { passive: false })
  canvas.addEventListener('dblclick', onDblClick)

  gl.enable(gl.DEPTH_TEST)
  gl.enable(gl.BLEND)
  gl.blendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
  gl.clearColor(0.063, 0.078, 0.094, 1)

  let rafId = 0
  let last = performance.now()
  function frame(now) {
    if (disposed) return
    const dt = (now - last) / 1000; last = now
    // auto-rotate: a slow turn about the screen-vertical axis (turntable feel)
    if (spin) rot = Q.norm(Q.mul(Q.axisAngle(0, 1, 0, dt * 0.4), rot))

    const w = canvas.clientWidth, h = canvas.clientHeight
    if (w > 0 && h > 0 && (canvas.width !== w * devicePixelRatio || canvas.height !== h * devicePixelRatio)) {
      canvas.width = w * devicePixelRatio; canvas.height = h * devicePixelRatio
    }
    gl.viewport(0, 0, canvas.width, canvas.height)
    gl.clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)

    if (nIndices) {
      const rotation = Q.toMat4(rot)
      const model = M.mul(rotation, M.mul(M.scale(fitScale), M.trans(-center[0], -center[1], -center[2])))
      const view = M.trans(panX, panY, -dist)
      const viewModel = M.mul(view, model)
      const proj = M.persp(0.9, (w || 1) / (h || 1), 0.05, 100)
      const mvp = M.mul(proj, viewModel)

      gl.useProgram(prog)
      gl.uniformMatrix4fv(uMVP, false, mvp)
      gl.uniformMatrix4fv(uViewModel, false, viewModel)
      gl.uniform1f(uMetallicFactor, metallicFactor)
      gl.uniform1f(uRoughnessFactor, roughnessFactor)
      if (colorMode === 2) {
        gl.activeTexture(gl.TEXTURE0)
        gl.bindTexture(gl.TEXTURE_2D, baseColorTexture)
        gl.uniform1i(uBaseColorTex, 0)
        gl.activeTexture(gl.TEXTURE1)
        gl.bindTexture(gl.TEXTURE_2D, mrTexture)
        gl.uniform1i(uMrTex, 1)
      }
      gl.bindVertexArray(vao)
      if (wire) {
        // hidden-line wireframe: a depth-only prepass (pushed back a hair via
        // polygon offset) occludes back-facing edges, so the front surface's
        // edges show instead of a see-through blob.
        gl.enable(gl.POLYGON_OFFSET_FILL)
        gl.polygonOffset(1.0, 1.0)
        gl.colorMask(false, false, false, false)
        gl.uniform1i(uWire, 0)
        gl.uniform1i(uColorMode, 0)
        gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, ibo)
        gl.drawElements(gl.TRIANGLES, nIndices, gl.UNSIGNED_INT, 0)
        gl.colorMask(true, true, true, true)
        gl.disable(gl.POLYGON_OFFSET_FILL)
        gl.uniform1i(uWire, 1)
        // Front-surface edges sit at ~equal depth to the offset-back fill, so
        // LEQUAL lets them pass while occluded edges (greater depth) fail.
        gl.depthFunc(gl.LEQUAL)
        gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, wireIbo)
        gl.drawElements(gl.LINES, nWire, gl.UNSIGNED_INT, 0)
        gl.depthFunc(gl.LESS)
      } else {
        gl.uniform1i(uWire, 0)
        gl.uniform1i(uColorMode, colorMode)
        gl.bindBuffer(gl.ELEMENT_ARRAY_BUFFER, ibo)
        gl.drawElements(gl.TRIANGLES, nIndices, gl.UNSIGNED_INT, 0)
      }
      gl.bindVertexArray(null)
    }
    rafId = requestAnimationFrame(frame)
  }
  rafId = requestAnimationFrame(frame)

  function dispose() {
    disposed = true
    cancelAnimationFrame(rafId)
    canvas.removeEventListener('mousedown', onMouseDown)
    window.removeEventListener('mouseup', onMouseUp)
    window.removeEventListener('mousemove', onMouseMove)
    canvas.removeEventListener('contextmenu', onContextMenu)
    canvas.removeEventListener('wheel', onWheel)
    canvas.removeEventListener('dblclick', onDblClick)
    canvas.removeEventListener('webglcontextlost', contextLost)
    dropTextures()
    gl.deleteBuffer(vbo); gl.deleteBuffer(nbo); gl.deleteBuffer(cbo)
    gl.deleteBuffer(mbo); gl.deleteBuffer(ubo); gl.deleteBuffer(ibo)
    gl.deleteBuffer(wireIbo)
    gl.deleteVertexArray(vao)
    gl.deleteProgram(prog)
    gl.getExtension('WEBGL_lose_context')?.loseContext()
  }

  return {
    setMesh,
    clear,
    dispose,
    resetView,
    setWire(v) { wire = v },
    setSpin(v) { spin = v },
    onSpinChanged(fn) { onSpinChange = fn },
  }
}

export default function GlbViewer({ blob }) {
  const { t } = useTranslation('media')
  const canvasRef = useRef(null)
  const viewerRef = useRef(null)
  const [glError, setGlError] = useState(null) // 'no-webgl2' | 'context-lost' | parse error text
  const [stats, setStats] = useState(null)
  const [wire, setWire] = useState(false)
  const [spin, setSpin] = useState(true)

  useEffect(() => {   // GL lifecycle — once per mount
    let viewer = null
    try {
      viewer = createGlbViewer(canvasRef.current, { onContextLost: () => setGlError('context-lost') })
    } catch {
      viewer = null
    }
    if (!viewer) {
      setGlError('no-webgl2')
      return undefined
    }
    viewer.onSpinChanged(setSpin)
    viewerRef.current = viewer
    return () => {
      viewerRef.current = null
      viewer.dispose()
    }
  }, [])

  useEffect(() => {   // (re)load when the blob changes
    if (!blob) {
      viewerRef.current?.clear()
      setStats(null)
      return undefined
    }
    let cancelled = false
    ;(async () => {
      try {
        // Parse before touching GL: stats and parse errors surface even when
        // WebGL2 is unavailable (e.g. headless CI), and the download button
        // keeps working either way.
        const mesh = parseGlb(await blob.arrayBuffer())
        if (cancelled) return
        setStats({
          nVerts: mesh.nVerts,
          nTris: mesh.nTris,
          pbr: !!(mesh.color0 || mesh.baseColorPng),
        })
        if (viewerRef.current) await viewerRef.current.setMesh(mesh)
      } catch (err) {
        if (!cancelled) setGlError(err.message)
      }
    })()
    return () => { cancelled = true }
  }, [blob])

  const toggleWire = () => {
    const next = !wire
    setWire(next)
    viewerRef.current?.setWire(next)
  }
  const toggleSpin = () => {
    const next = !spin
    setSpin(next)
    viewerRef.current?.setSpin(next)
  }

  return (
    <div className="glb-viewer" style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-sm)', width: '100%' }}>
      <canvas
        ref={canvasRef}
        style={{ width: '100%', aspectRatio: '4 / 3', borderRadius: 'var(--radius-md)', background: '#101418', touchAction: 'none' }}
        data-testid="glb-canvas"
      />
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', flexWrap: 'wrap' }}>
        <button type="button" className={`btn btn-sm ${wire ? 'btn-primary' : 'btn-secondary'}`} onClick={toggleWire}>
          <i className="fas fa-border-none" /> {t('threed.viewer.wireframe')}
        </button>
        <button type="button" className={`btn btn-sm ${spin ? 'btn-primary' : 'btn-secondary'}`} onClick={toggleSpin}>
          <i className="fas fa-rotate" /> {t('threed.viewer.autoRotate')}
        </button>
        {stats && (
          <span style={{ color: 'var(--color-text-muted)', fontSize: '0.85em' }} data-testid="glb-stats">
            {t('threed.viewer.stats', { verts: stats.nVerts.toLocaleString(), tris: stats.nTris.toLocaleString() })}
            {stats.pbr ? ' · PBR' : ''}
          </span>
        )}
      </div>
      {glError === 'no-webgl2' && <p style={{ color: 'var(--color-text-muted)' }}>{t('threed.viewer.noWebgl')}</p>}
      {glError === 'context-lost' && <p style={{ color: 'var(--color-text-muted)' }}>{t('threed.viewer.contextLost')}</p>}
      {glError && glError !== 'no-webgl2' && glError !== 'context-lost' && (
        <p style={{ color: 'var(--color-danger, #e5484d)' }}>{glError}</p>
      )}
      <p style={{ color: 'var(--color-text-muted)', fontSize: '0.8em', margin: 0 }}>{t('threed.viewer.hint')}</p>
    </div>
  )
}
