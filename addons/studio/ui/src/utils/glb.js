// Minimal GLB (binary glTF 2.0) parser for the two forms trellis2cpp's
// t2_bake_glb emits (see mesh_export.cpp in localai-org/trellis2cpp):
//
//  Form B (default, dense vertex PBR):
//    POSITION (VEC3 f32), NORMAL (VEC3 f32),
//    COLOR_0 (VEC4 u16 normalized, LINEAR color + alpha),
//    _METALLIC_ROUGHNESS (VEC2 u8 normalized: metallic, roughness),
//    indices (SCALAR u32); material carries average metallic/roughness factors.
//
//  Form A (opt-in via T2GLB_XATLAS, UV atlas):
//    POSITION/NORMAL/TEXCOORD_0 f32 + u32 indices,
//    baseColorTexture (sRGB PNG) + metallicRoughnessTexture
//    (glTF convention: G=roughness, B=metallic).
//
// Deliberately unsupported (the baker never emits them; anything else throws
// so the page can show the error while the download button still works):
// sparse accessors, interleaved bufferViews (byteStride), Draco, multiple
// meshes/primitives.

const GLB_MAGIC = 0x46546c67
const CHUNK_JSON = 0x4e4f534a
const CHUNK_BIN = 0x004e4942

const COMPONENT_ARRAYS = {
  5121: Uint8Array,
  5123: Uint16Array,
  5125: Uint32Array,
  5126: Float32Array,
}

const TYPE_SIZES = { SCALAR: 1, VEC2: 2, VEC3: 3, VEC4: 4 }

export function parseGlb(buf) {
  if (!(buf instanceof ArrayBuffer) || buf.byteLength < 20) throw new Error('not a GLB file')
  const dv = new DataView(buf)
  if (dv.getUint32(0, true) !== GLB_MAGIC) throw new Error('not a GLB file')

  let offset = 12
  let json = null
  let binOffset = -1
  let binLength = 0
  while (offset + 8 <= buf.byteLength) {
    const len = dv.getUint32(offset, true)
    const type = dv.getUint32(offset + 4, true)
    const payload = offset + 8
    if (type === CHUNK_JSON && !json) {
      json = JSON.parse(new TextDecoder().decode(new Uint8Array(buf, payload, len)))
    } else if (type === CHUNK_BIN && binOffset < 0) {
      binOffset = payload
      binLength = len
    }
    offset = payload + len + ((4 - (len % 4)) % 4)
  }
  if (!json) throw new Error('GLB has no JSON chunk')

  const accessor = (index) => {
    const a = json.accessors?.[index]
    if (!a) throw new Error(`missing accessor ${index}`)
    if (a.sparse) throw new Error('sparse accessors not supported')
    const view = json.bufferViews?.[a.bufferView]
    if (!view) throw new Error(`missing bufferView ${a.bufferView}`)
    if (view.byteStride) throw new Error('interleaved bufferViews not supported')
    const ArrayType = COMPONENT_ARRAYS[a.componentType]
    const size = TYPE_SIZES[a.type]
    if (!ArrayType || !size) throw new Error(`unsupported accessor layout ${a.componentType}/${a.type}`)
    const start = binOffset + (view.byteOffset || 0) + (a.byteOffset || 0)
    if (binOffset < 0 || start + a.count * size * ArrayType.BYTES_PER_ELEMENT > binOffset + binLength) {
      throw new Error('accessor outside the BIN chunk')
    }
    return {
      array: new ArrayType(buf, start, a.count * size),
      size,
      normalized: !!a.normalized,
      min: a.min,
      max: a.max,
      count: a.count,
    }
  }

  const prim = json.meshes?.[0]?.primitives?.[0]
  if (!prim) throw new Error('GLB has no mesh')
  const attrs = prim.attributes || {}
  if (attrs.POSITION === undefined) throw new Error('GLB mesh has no POSITION attribute')

  const position = accessor(attrs.POSITION)
  const normal = attrs.NORMAL !== undefined ? accessor(attrs.NORMAL) : null

  let indices
  if (prim.indices !== undefined) {
    const idx = accessor(prim.indices)
    indices = idx.array instanceof Uint32Array ? idx.array : Uint32Array.from(idx.array)
  } else {
    indices = new Uint32Array(position.count)
    for (let i = 0; i < indices.length; i++) indices[i] = i
  }

  const color0 = attrs.COLOR_0 !== undefined ? accessor(attrs.COLOR_0) : null
  const metalRough = attrs._METALLIC_ROUGHNESS !== undefined ? accessor(attrs._METALLIC_ROUGHNESS) : null
  const uv = attrs.TEXCOORD_0 !== undefined ? accessor(attrs.TEXCOORD_0) : null

  const material = json.materials?.[prim.material]?.pbrMetallicRoughness || {}
  const imageBytes = (textureIndex) => {
    if (textureIndex === undefined) return null
    const source = json.textures?.[textureIndex]?.source
    const view = json.bufferViews?.[json.images?.[source]?.bufferView]
    if (!view) return null
    return new Uint8Array(buf, binOffset + (view.byteOffset || 0), view.byteLength)
  }

  // POSITION min/max are mandatory in glTF, but compute a fallback so a
  // technically-invalid file still frames correctly.
  let bboxMin = position.min
  let bboxMax = position.max
  if (!bboxMin || !bboxMax) {
    bboxMin = [Infinity, Infinity, Infinity]
    bboxMax = [-Infinity, -Infinity, -Infinity]
    for (let i = 0; i < position.array.length; i += 3) {
      for (let k = 0; k < 3; k++) {
        const v = position.array[i + k]
        if (v < bboxMin[k]) bboxMin[k] = v
        if (v > bboxMax[k]) bboxMax[k] = v
      }
    }
  }

  return {
    positions: position.array,
    normals: normal ? normal.array : null,
    indices,
    color0,
    metalRough,
    uv: uv ? uv.array : null,
    baseColorPng: imageBytes(material.baseColorTexture?.index),
    metalRoughPng: imageBytes(material.metallicRoughnessTexture?.index),
    material: {
      metallicFactor: material.metallicFactor ?? 1,
      roughnessFactor: material.roughnessFactor ?? 1,
    },
    bboxMin,
    bboxMax,
    nVerts: position.count,
    nTris: Math.floor(indices.length / 3),
  }
}
