// Package config is the terminal app's settings file.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Target is one place the library mirrors into. Kind is "folder",
// "obsidian" or "applenotes"; Path is the folder (for Obsidian, the vault's
// subfolder), Folder the Apple Notes folder name.
type Target struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Path   string `json:"path,omitempty"`
	Folder string `json:"folder,omitempty"`
}

// Config is config.json.
type Config struct {
	Library string `json:"library"`
	// Editor is "" for the built-in editor or "external" for $EDITOR.
	Editor       string   `json:"editor,omitempty"`
	Sort         string   `json:"sort,omitempty"`
	TrashDays    int      `json:"trashDays,omitempty"`
	WordGoal     int      `json:"wordGoal,omitempty"`
	Targets      []Target `json:"targets,omitempty"`
	WhisperModel string   `json:"whisperModel,omitempty"`
	LlamaModel   string   `json:"llamaModel,omitempty"`
	Language     string   `json:"language,omitempty"`
}

// Dir is where config.json lives: $XDG_CONFIG_HOME/textarea-cli when that
// is set, on every system, and the OS's own config folder otherwise.
func Dir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "textarea-cli")
	}
	d, err := os.UserConfigDir()
	if err != nil {
		d = "."
	}
	return filepath.Join(d, "textarea-cli")
}

// Path is the settings file.
func Path() string { return filepath.Join(Dir(), "config.json") }

// DefaultLibrary is ~/Documents/Textarea, or $TEXT_LIBRARY when set.
func DefaultLibrary() string {
	if p := os.Getenv("TEXT_LIBRARY"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "Textarea"
	}
	return filepath.Join(home, "Documents", "Textarea")
}

// Load reads the settings, filling defaults for anything missing.
func Load() (*Config, error) {
	c := &Config{}
	b, err := os.ReadFile(Path())
	if err == nil {
		if err := json.Unmarshal(b, c); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if env := os.Getenv("TEXT_LIBRARY"); env != "" {
		c.Library = env
	}
	if c.Library == "" {
		c.Library = DefaultLibrary()
	}
	if c.TrashDays == 0 {
		c.TrashDays = 30
	}
	if c.Sort == "" {
		c.Sort = "updated"
	}
	return c, nil
}

// Save writes the settings.
func (c *Config) Save() error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(Path(), append(b, '\n'), 0o644)
}

// Target finds a target by name.
func (c *Config) Target(name string) (Target, bool) {
	for _, t := range c.Targets {
		if t.Name == name {
			return t, true
		}
	}
	return Target{}, false
}

// SetTarget adds or replaces a target by name.
func (c *Config) SetTarget(t Target) {
	for i := range c.Targets {
		if c.Targets[i].Name == t.Name {
			c.Targets[i] = t
			return
		}
	}
	c.Targets = append(c.Targets, t)
}

// RemoveTarget drops a target by name.
func (c *Config) RemoveTarget(name string) {
	out := c.Targets[:0]
	for _, t := range c.Targets {
		if t.Name != name {
			out = append(out, t)
		}
	}
	c.Targets = out
}
