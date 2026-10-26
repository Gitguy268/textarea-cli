//go:build darwin

package target

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// systemNotes talks to Notes.app through JavaScript for Automation. The
// first call makes macOS ask whether this terminal may control Notes; until
// that is allowed, every call fails with error -1743.
type systemNotes struct{}

// The script reads its request from a file named in argv and prints JSON.
// Everything stays inside the one named folder of the default account,
// which is created by the first write.
const notesScript = `
function run(argv) {
  const Notes = Application('Notes')
  const raw = $.NSString.stringWithContentsOfFileEncodingError(argv[0], $.NSUTF8StringEncoding, null)
  const req = JSON.parse(ObjC.unwrap(raw))
  const account = Notes.defaultAccount()
  let folder = account.folders.whose({name: req.folder})
  if (req.op === 'list') {
    // A folder that isn't there yet is empty; listing (and so a dry run)
    // never creates it.
    if (folder.length === 0) return '[]'
    const notes = folder[0].notes
    const ids = notes.id()
    const locked = notes.passwordProtected()
    const created = notes.creationDate()
    const modified = notes.modificationDate()
    const out = []
    for (let i = 0; i < ids.length; i++) {
      out.push({
        id: ids[i],
        locked: locked[i],
        created: created[i].getTime(),
        modified: modified[i].getTime(),
        body: locked[i] ? '' : Notes.notes.byId(ids[i]).body(),
      })
    }
    return JSON.stringify(out)
  }
  if (folder.length === 0) {
    account.folders.push(Notes.Folder({name: req.folder}))
    folder = account.folders.whose({name: req.folder})
  }
  folder = folder[0]
  const results = []
  for (const op of req.ops) {
    try {
      if (op.op === 'create') {
        const made = Notes.make({new: 'note', at: folder, withProperties: {body: op.html}})
        results.push({id: made.id(), modified: made.modificationDate().getTime()})
      } else if (op.op === 'update') {
        const n = Notes.notes.byId(op.id)
        n.body = op.html
        results.push({id: op.id, modified: n.modificationDate().getTime()})
      } else if (op.op === 'delete') {
        Notes.delete(Notes.notes.byId(op.id))
        results.push({id: op.id, modified: 0})
      }
    } catch (error) {
      results.push({id: op.id || '', modified: 0, error: String(error)})
    }
  }
  return JSON.stringify(results)
}
`

func runNotes(req any, out any) error {
	f, err := os.CreateTemp("", "textarea-notes-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	json.NewEncoder(f).Encode(req)
	f.Close()
	cmd := exec.Command("osascript", "-l", "JavaScript", "-e", notesScript, f.Name())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, "-1743") {
			return fmt.Errorf("macOS has not allowed this terminal to control Notes: System Settings > Privacy & Security > Automation")
		}
		return fmt.Errorf("Apple Notes: %s", msg)
	}
	return json.Unmarshal(bytes.TrimSpace(b), out)
}

func (systemNotes) List(folder string) ([]AppleNote, error) {
	var out []AppleNote
	err := runNotes(map[string]any{"op": "list", "folder": folder}, &out)
	return out, err
}

func (systemNotes) Apply(folder string, ops []NotesOp) ([]NotesResult, error) {
	var out []NotesResult
	err := runNotes(map[string]any{"op": "apply", "folder": folder, "ops": ops}, &out)
	return out, err
}
