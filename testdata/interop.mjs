// The web app's Folder sync, run under Node against a real folder: the same
// gather + planSync + reconcile steps as lib/folder-sync.js, with the
// browser's directory handle and IndexedDB library swapped for node:fs and a
// JSON file. The interop test points it at a textarea-cli library.
//
//	node interop.mjs <state.json> <folder> pass
//	node interop.mjs <state.json> <folder> put '<json entry>'
//
// TEXTAREA_DIR points at a textarea-master-claude tree.

import {existsSync, readdirSync, readFileSync, statSync, unlinkSync, writeFileSync} from 'node:fs'
import {join} from 'node:path'
import {pathToFileURL} from 'node:url'

const root = process.env.TEXTAREA_DIR
const lib = (name) => import(pathToFileURL(join(root, 'lib', name)).href)
const sync = await lib('folder-sync.js')
const {sanitizeStyle} = await lib('style.js')

const [statePath, folder, op, arg] = process.argv.slice(2)
const state = existsSync(statePath) ? JSON.parse(readFileSync(statePath, 'utf8')) : {entries: {}, manifest: {}}

function fileOf(path) {
  const stat = statSync(path)
  return {
    size: stat.size,
    lastModified: Math.floor(stat.mtimeMs),
    text: async () => readFileSync(path, 'utf8'),
    slice: (a, b) => ({text: async () => readFileSync(path).subarray(a, b).toString('utf8')}),
  }
}

const dir = {
  async *values() {
    for (const d of readdirSync(folder, {withFileTypes: true})) {
      yield {kind: d.isFile() ? 'file' : 'directory', name: d.name, getFile: async () => fileOf(join(folder, d.name))}
    }
  },
  async getFileHandle(name) {
    const path = join(folder, name)
    return {
      getFile: async () => fileOf(path),
      createWritable: async () => {
        let text = ''
        return {write: async (t) => void (text += t), close: async () => writeFileSync(path, text)}
      },
    }
  },
  async removeEntry(name) {
    unlinkSync(join(folder, name))
  },
}

// library.js putDocument, minus IndexedDB.
function putDocument(entry) {
  const existing = state.entries[entry.id]
  const now = Date.now()
  const record = {
    id: entry.id,
    title: entry.title || 'Untitled note',
    text: entry.text ?? '',
    style: entry.style ?? null,
    hash: entry.hash ?? '',
    created: existing?.created ?? entry.created ?? now,
    updated: entry.updated ?? now,
    words: entry.words ?? 0,
  }
  state.entries[record.id] = record
  return record
}

// folder-sync.js reconcile, minus the lock, gate and notices.
async function pass() {
  const entries = Object.values(state.entries).sort((a, b) => b.updated - a.updated)
  const files = await sync.gather(dir)
  const plan = sync.planSync({entries, files, manifest: state.manifest})
  const counts = {written: 0, imported: 0, removed: 0}
  for (const action of plan.actions) {
    if (action.type === 'write') {
      const handle = await dir.getFileHandle(action.name)
      const w = await handle.createWritable()
      await w.write(action.text)
      await w.close()
      if (action.remove && action.remove !== action.name) await dir.removeEntry(action.remove).catch(() => {})
      counts.written++
    } else if (action.type === 'import') {
      putDocument({...action.record, style: sanitizeStyle(action.record.style)})
      counts.imported++
    } else if (action.type === 'remove-file') {
      await dir.removeEntry(action.name).catch(() => {})
      counts.removed++
    } else if (action.type === 'remove-note') {
      delete state.entries[action.id]
      counts.removed++
    }
  }
  for (const [id, slot] of Object.entries(plan.manifest)) {
    if (slot.fileUpdated !== null) continue
    try {
      slot.fileUpdated = (await (await dir.getFileHandle(slot.name)).getFile()).lastModified
    } catch {
      delete plan.manifest[id]
    }
  }
  state.manifest = plan.manifest
  return counts
}

let out
if (op === 'pass') out = await pass()
else if (op === 'put') out = putDocument(JSON.parse(arg))
else throw new Error('unknown op ' + op)
writeFileSync(statePath, JSON.stringify(state))
process.stdout.write(JSON.stringify({result: out, entries: state.entries}))
