//go:build !darwin

package target

type systemNotes struct{}

func (systemNotes) List(string) ([]AppleNote, error) { return nil, ErrNoNotes }

func (systemNotes) Apply(string, []NotesOp) ([]NotesResult, error) { return nil, ErrNoNotes }
