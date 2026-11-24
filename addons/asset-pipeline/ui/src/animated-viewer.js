// SPDX-License-Identifier: MIT
import * as THREE from 'three'
import { GLTFLoader } from 'three/addons/loaders/GLTFLoader.js'
import { OrbitControls } from 'three/addons/controls/OrbitControls.js'
import { createRigInspection } from './rig-inspection.js'

export function playableClips(clips) {
  return clips.filter(clip => Number.isFinite(clip.duration) && clip.duration > 0)
}

export function inspectGLB(buffer) {
  if (!(buffer instanceof ArrayBuffer) || buffer.byteLength < 20 || buffer.byteLength > 512 * 1024 * 1024) throw new Error('Invalid or oversized GLB.')
  const view = new DataView(buffer)
  if (view.getUint32(0, true) !== 0x46546c67 || view.getUint32(4, true) !== 2 || view.getUint32(8, true) !== buffer.byteLength || view.getUint32(16, true) !== 0x4e4f534a) throw new Error('Invalid GLB 2.0 header.')
  const length = view.getUint32(12, true)
  if (length > 32 * 1024 * 1024 || length > buffer.byteLength - 20) throw new Error('Invalid GLB metadata length.')
  const json = JSON.parse(new TextDecoder().decode(new Uint8Array(buffer, 20, length)))
  if (json.asset?.version !== '2.0' || (json.nodes?.length || 0) > 4096) throw new Error('Unsupported GLB scene.')
  for (const item of [...json.buffers || [], ...json.images || []]) {
    if (item.uri && !/^data:image\/(png|jpeg|webp);base64,/.test(item.uri)) throw new Error('External GLB resources are not allowed in pipeline previews.')
  }
  return json
}

function disposeScene(root) {
  const geometries = new Set(), materials = new Set(), textures = new Set(), skeletons = new Set()
  root?.traverse(object => {
    if (object.geometry) geometries.add(object.geometry)
    if (object.skeleton) skeletons.add(object.skeleton)
    for (const material of Array.isArray(object.material) ? object.material : object.material ? [object.material] : []) materials.add(material)
  })
  for (const material of materials) {
    for (const value of Object.values(material)) if (value?.isTexture) textures.add(value)
    material.dispose()
  }
  for (const texture of textures) { texture.dispose(); texture.source?.data?.close?.() }
  for (const skeleton of skeletons) skeleton.dispose()
  for (const geometry of geometries) geometry.dispose()
}

export async function createAnimatedViewer(canvas, buffer, { onState = () => {}, onContextLost = () => {} } = {}) {
  const json = inspectGLB(buffer)
  const manager = new THREE.LoadingManager()
  manager.setURLModifier(url => {
    if (/^(blob:|data:image\/(png|jpeg|webp);base64,)/.test(url)) return url
    throw new Error('External preview resources are disabled.')
  })
  const gltf = await new GLTFLoader(manager).parseAsync(buffer, '')
  let renderer, controls, observer, visibility, frameID = 0, disposed = false
  const scene = new THREE.Scene()
  scene.background = new THREE.Color('#101820')
  scene.add(gltf.scene)
  const rig = createRigInspection(gltf.scene)
  if (rig.helper) scene.add(rig.helper)
  const mixer = new THREE.AnimationMixer(gltf.scene)
  const clips = playableClips(gltf.animations)
  let clipIndex = 0, action, playing = false, visible = true, dirty = true, last = 0, reported = 0, follow = true
  const duration = () => clips[clipIndex]?.duration || 0
  const state = () => onState({ playing, follow, time: action?.time || 0, duration: duration(), clip: clipIndex, clips: clips.map((clip, i) => clip.name || `Clip ${i + 1}`), rig: rig.state() })
  const camera = new THREE.PerspectiveCamera(45, 1, 0.01, 1000)
  const points = [], pairs = []
  let anchor, initialCenter, frameSize
  const previousAnchor = new THREE.Vector3(), initialAnchor = new THREE.Vector3()
  let lines
  function updatePose() {
    gltf.scene.updateMatrixWorld(true)
    if (anchor && controls) {
      const current = anchor.getWorldPosition(new THREE.Vector3())
      if (follow) {
        const delta = current.clone().sub(previousAnchor)
        camera.position.add(delta); controls.target.add(delta)
      }
      previousAnchor.copy(current)
    }
    if (lines) {
      const array = lines.geometry.attributes.position.array
      const point = new THREE.Vector3()
      pairs.forEach(([a, b], i) => {
        a.getWorldPosition(point).toArray(array, i * 6)
        b.getWorldPosition(point).toArray(array, i * 6 + 3)
      })
      lines.geometry.attributes.position.needsUpdate = true
      lines.geometry.computeBoundingSphere()
    }
  }
  function resetView() {
    if (!controls || !initialCenter) return
    const center = initialCenter.clone()
    if (anchor) center.add(anchor.getWorldPosition(new THREE.Vector3()).sub(initialAnchor))
    controls.target.copy(center)
    const aspect = Math.max(canvas.clientWidth, 1) / Math.max(canvas.clientHeight, 1)
    const distance = frameSize * 1.5 * Math.max(1, 1 / aspect)
    camera.position.copy(center).add(new THREE.Vector3(frameSize * 0.25, frameSize * 0.1, distance))
    controls.update(); request()
  }
  function request() {
    dirty = true
    if (!disposed && visible && !document.hidden && !frameID) frameID = requestAnimationFrame(draw)
  }
  function draw(now) {
    frameID = 0
    if (disposed || !visible || document.hidden) { last = 0; return }
    if (last && now - last < 1000 / 30) { frameID = requestAnimationFrame(draw); return }
    const delta = last ? Math.min((now - last) / 1000, 0.1) : 0
    last = now
    if (playing) { mixer.update(delta); updatePose(); dirty = true }
    if (dirty) {
      const width = Math.max(canvas.clientWidth, 1), height = Math.max(canvas.clientHeight, 1)
      renderer.setSize(width, height, false)
      camera.aspect = width / height; camera.updateProjectionMatrix()
      renderer.render(scene, camera)
      dirty = false
    }
    if (now - reported > 200) { state(); reported = now }
    if (playing) frameID = requestAnimationFrame(draw)
    else last = 0
  }
  const lost = event => { event.preventDefault(); playing = false; onContextLost(); state() }
  function dispose() {
    if (disposed) return
    disposed = true
    cancelAnimationFrame(frameID)
    observer?.disconnect(); visibility?.disconnect()
    document.removeEventListener('visibilitychange', request)
    canvas.removeEventListener('webglcontextlost', lost)
    controls?.dispose()
    mixer.stopAllAction(); mixer.uncacheRoot(gltf.scene)
    rig.dispose()
    disposeScene(scene)
    renderer?.dispose(); renderer?.forceContextLoss()
  }
  try {
    renderer = new THREE.WebGLRenderer({ canvas, antialias: true, alpha: false })
    renderer.setPixelRatio(Math.min(devicePixelRatio || 1, 1.5))
    renderer.toneMapping = THREE.ACESFilmicToneMapping
    scene.add(new THREE.HemisphereLight(0xddeeff, 0x444444, 2.5))
    const key = new THREE.DirectionalLight(0xffffff, 3)
    key.position.set(3, 5, 4); scene.add(key)
    let vertices = 0, triangles = 0, meshes = 0
    gltf.scene.traverse(object => {
      if (!object.isMesh) return
      meshes++
      vertices += object.geometry.attributes.position?.count || 0
      triangles += (object.geometry.index?.count || object.geometry.attributes.position?.count || 0) / 3
      // Some generated topology has mixed winding. Keep both sides inspectable.
      for (const material of Array.isArray(object.material) ? object.material : [object.material]) material.side = THREE.DoubleSide
    })
    if (!meshes) {
      const nodes = await Promise.all((json.nodes || []).map((_, index) => gltf.parser.getDependency('node', index)))
      json.nodes?.forEach((node, i) => {
        for (const child of node.children || []) if (nodes[child]) pairs.push([nodes[i], nodes[child]])
      })
      if (!pairs.length) throw new Error('This GLB has neither a mesh nor a connected motion skeleton.')
      const geometry = new THREE.BufferGeometry()
      geometry.setAttribute('position', new THREE.BufferAttribute(new Float32Array(pairs.length * 6), 3))
      lines = new THREE.LineSegments(geometry, new THREE.LineBasicMaterial({ color: 0x7dd3fc }))
      lines.frustumCulled = false
      scene.add(lines)
      points.push(...nodes)
    }
    if (clips.length) { action = mixer.clipAction(clips[0]); action.play(); mixer.setTime(0) }
    else if (gltf.animations.length) {
      // Some rig exporters encode the rest pose as a zero-duration clip.
      // Apply that pose without offering playback controls for a static rig.
      mixer.clipAction(gltf.animations[0]).play()
      mixer.setTime(0)
    }
    if (json.nodes?.length) anchor = await gltf.parser.getDependency('node', json.skins?.[0]?.joints?.[0] ?? 0)
    updatePose()
    const bounds = new THREE.Box3().setFromObject(gltf.scene)
    const point = new THREE.Vector3()
    for (const node of points) bounds.expandByPoint(node.getWorldPosition(point))
    if (bounds.isEmpty()) throw new Error('The GLB has no visible geometry.')
    const center = bounds.getCenter(new THREE.Vector3()), size = Math.max(bounds.getSize(new THREE.Vector3()).length(), 0.1)
    initialCenter = center.clone(); frameSize = size
    if (anchor) { anchor.getWorldPosition(initialAnchor); previousAnchor.copy(initialAnchor) }
    camera.near = size / 1000; camera.far = size * 100
    camera.position.copy(center).add(new THREE.Vector3(size * 0.4, size * 0.2, size * 1.3))
    controls = new OrbitControls(camera, canvas)
    controls.target.copy(center); controls.update(); controls.addEventListener('change', request)
    resetView()
    observer = new ResizeObserver(request); observer.observe(canvas)
    visibility = new IntersectionObserver(entries => { visible = entries[0]?.isIntersecting !== false; last = 0; if (visible) request(); else { cancelAnimationFrame(frameID); frameID = 0 } })
    visibility.observe(canvas)
    document.addEventListener('visibilitychange', request)
    canvas.addEventListener('webglcontextlost', lost)
    state(); request()
    return {
      stats: meshes ? `${Math.floor(triangles).toLocaleString()} triangles · ${vertices.toLocaleString()} vertices${rig.bones.length ? ` · ${rig.bones.length} joints` : ''}` : `${points.length} joints · skeleton motion`,
      setSkeleton(value) { rig.setSkeleton(value); state(); request() },
      setDisplayMode(value) { rig.setMode(value); state(); request() },
      setJoint(value) { rig.setJoint(value); state(); request() },
      setPlaying(value) { playing = !!value && !!action; last = 0; state(); request() },
      setFollow(value) { follow = !!value; if (follow) resetView(); state(); request() },
      resetView,
      seek(value) { if (action) { mixer.setTime(Math.max(0, Math.min(value, duration() - 0.000001))); updatePose(); state(); request() } },
      setClip(index) {
        if (!clips[index]) return
        mixer.stopAllAction(); clipIndex = index
        action = mixer.clipAction(clips[index]); action.reset().play(); mixer.setTime(0)
        updatePose(); state(); request()
      },
      dispose,
    }
  } catch (error) { dispose(); throw error }
}
