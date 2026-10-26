// The browser app's own code, run under Node, as the reference the Go port
// is checked against. Reads a JSON array of requests on stdin and writes one
// result per request. TEXTAREA_DIR points at a textarea-master-claude tree.

import {readFileSync} from 'node:fs'
import {join} from 'node:path'
import {pathToFileURL} from 'node:url'

const root = process.env.TEXTAREA_DIR
const lib = (name) => import(pathToFileURL(join(root, 'lib', name)).href)

const fm = await lib('front-matter.js')
const share = await lib('share.js')
const stats = await lib('stats.js')
const md = await lib('markdown.js')
const formats = await lib('formats.js')
const sync = await lib('folder-sync.js')

const requests = JSON.parse(readFileSync(0, 'utf8'))
const out = []
for (const r of requests) {
  switch (r.op) {
    case 'parse':
      out.push(fm.parseNote(r.source))
      break
    case 'render':
      out.push(fm.renderNote({...r.note, style: r.note.style || null}))
      break
    case 'title':
      out.push(fm.titleOf(r.text))
      break
    case 'filename':
      out.push(fm.filenameFor(r.title, new Set(r.taken), r.own || null))
      break
    case 'stats':
      out.push(stats.countStats(r.text))
      break
    case 'tags':
      out.push(md.tagsIn(r.text))
      break
    case 'tasks':
      out.push(md.tasksIn(r.text))
      break
    case 'wiki':
      out.push(md.wikiTitles(r.text))
      break
    case 'convert':
      out.push(formats.convert({...r.note, style: r.note.style || null}, r.format).text)
      break
    case 'encode':
      out.push(await share.encodeHash(r.content))
      break
    case 'decode':
      out.push(await share.decodeHash(r.hash))
      break
    case 'encrypt':
      out.push(await share.encryptHash(r.content, r.password))
      break
    case 'decrypt':
      out.push(await share.decryptHash(r.payload, r.password))
      break
    case 'plan': {
      const entries = r.entries.map((e) => ({...e, style: e.style || null}))
      const plan = sync.planSync({entries, files: r.files, manifest: r.manifest})
      out.push(plan)
      break
    }
    default:
      throw new Error('unknown op ' + r.op)
  }
}
process.stdout.write(JSON.stringify(out))
