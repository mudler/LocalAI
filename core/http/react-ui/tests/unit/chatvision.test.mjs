import assert from 'node:assert/strict'
import test from 'node:test'

import { declaresVision, isImageFile, seesImages, visionNotice, withVision } from '../../src/utils/chatVision.js'

const vl = { id: 'cloud-vl', capabilities: ['chat', 'vision'] }
const text = { id: 'cloud', capabilities: ['chat'] }
const photo = { name: 'photo.jpg', type: 'image/jpeg' }
const notes = { name: 'notes.txt', type: 'text/plain' }

test('a model sees images when its capabilities say vision', () => {
  assert.equal(seesImages(vl), true)
  assert.equal(seesImages({ capabilities: ['FLAG_CHAT', 'FLAG_VISION'] }), true)
  assert.equal(seesImages(text), false)
  assert.equal(seesImages(undefined), false)
})

test('a file is an image by its type or, without one, by its name', () => {
  assert.equal(isImageFile(photo), true)
  assert.equal(isImageFile({ name: 'scan.WEBP', type: '' }), true)
  assert.equal(isImageFile(notes), false)
  assert.equal(isImageFile({ name: 'clip.mp4', type: 'video/mp4' }), false)
})

test('no notice when no image is attached or the model sees images', () => {
  assert.equal(visionNotice({ files: [notes], model: 'cloud', models: [text], imageModel: 'helper' }), null)
  assert.equal(visionNotice({ files: [photo], model: 'cloud-vl', models: [vl, text], imageModel: '' }), null)
  assert.equal(visionNotice({ files: [], model: 'cloud', models: [text], imageModel: '' }), null)
})

test('no notice while the model list is not known yet', () => {
  assert.equal(visionNotice({ files: [photo], model: 'cloud', models: [], imageModel: '' }), null)
  assert.equal(visionNotice({ files: [photo], model: '', models: [text], imageModel: '' }), null)
})

test('the notice names the model that describes the image', () => {
  assert.deepEqual(visionNotice({ files: [notes, photo], model: 'cloud', models: [text], imageModel: 'helper-vl' }),
    { key: 'describedBy', model: 'helper-vl' })
})

test('the notice says when nothing can describe the image', () => {
  assert.deepEqual(visionNotice({ files: [photo], model: 'cloud', models: [text], imageModel: '' }), { key: 'none', model: '' })
})

test('the proxy switch reads and writes vision in known_usecases', () => {
  assert.equal(declaresVision(['chat']), false)
  assert.equal(declaresVision(['chat', 'vision']), true)
  assert.equal(declaresVision(['FLAG_CHAT', 'FLAG_VISION']), true)
  assert.equal(declaresVision(undefined), false)

  assert.deepEqual(withVision(['chat'], true), ['chat', 'vision'])
  assert.deepEqual(withVision(['chat', 'vision'], true), ['chat', 'vision'])
  assert.deepEqual(withVision(undefined, true), ['chat', 'vision'])
  assert.deepEqual(withVision(['chat', 'vision'], false), ['chat'])
  assert.deepEqual(withVision(['FLAG_CHAT', 'FLAG_VISION'], false), ['FLAG_CHAT'])
})
