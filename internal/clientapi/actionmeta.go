package clientapi

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/mrjvadi/torncity/internal/commands"
)

// The kinds a client honours for an action's kind, so it can pick a colour
// and a role instead of drawing every button the same way. "back" is part of
// the contract clients are told about (api/client-api.md) even though no
// command in configs/actions.yml uses it today: every screen's own
// list/hub/view command is what a "back to X" button on a deeper screen
// sends, and that command is already "navigation" — a client is free to
// render a navigation action pointed at an ancestor screen as a back button.
const (
	KindPrimary    = "primary"
	KindSecondary  = "secondary"
	KindDanger     = "danger"
	KindNavigation = "navigation"
	KindBack       = "back"
	KindConfirm    = "confirm"
)

func validKind(k string) bool {
	switch k {
	case KindPrimary, KindSecondary, KindDanger, KindNavigation, KindBack, KindConfirm:
		return true
	}
	return false
}

// ActionMeta is one command's line in configs/actions.yml: how a client
// should draw the button that sends it.
type ActionMeta struct {
	Kind  string `yaml:"kind"`
	Icon  string `yaml:"icon"`
	Group string `yaml:"group,omitempty"`
}

// ActionMetadata is configs/actions.yml, loaded. The zero value answers
// every command with the safe default (secondary, action:default), so a nil
// *ActionMetadata never blocks an action from reaching a client.
type ActionMetadata struct {
	defaults ActionMeta
	commands map[string]ActionMeta
}

// actionMetaFile mirrors configs/actions.yml.
type actionMetaFile struct {
	Defaults ActionMeta            `yaml:"defaults"`
	Commands map[string]ActionMeta `yaml:"commands"`
}

// Failures LoadActionMetadata reports.
var (
	ErrActionMetaSyntax  = errors.New("clientapi: actions.yml is not valid")
	ErrActionMetaKind    = errors.New("clientapi: a line names no valid kind")
	ErrActionMetaIcon    = errors.New("clientapi: a line names no icon")
	ErrActionMetaCommand = errors.New("clientapi: a line names no command the game serves")
)

// LoadActionMetadata reads the table.
func LoadActionMetadata(path string) (*ActionMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("clientapi: read %s: %w", path, err)
	}
	return ParseActionMetadata(data)
}

// ParseActionMetadata reads the table from its yaml. Every line must name a
// command the game serves to players and a valid kind, and every entry
// (defaults included) must name an icon; the first problem of each kind is
// reported with its command.
func ParseActionMetadata(data []byte) (*ActionMetadata, error) {
	var f actionMetaFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrActionMetaSyntax, err)
	}
	m := &ActionMetadata{defaults: f.Defaults, commands: map[string]ActionMeta{}}
	var problems []error
	if !validKind(f.Defaults.Kind) {
		problems = append(problems, fmt.Errorf("%w: defaults: %q", ErrActionMetaKind, f.Defaults.Kind))
	}
	if f.Defaults.Icon == "" {
		problems = append(problems, fmt.Errorf("%w: defaults", ErrActionMetaIcon))
	}
	for command, meta := range f.Commands {
		if !commands.FromPlayerCommand(command) {
			problems = append(problems, fmt.Errorf("%w: %q", ErrActionMetaCommand, command))
			continue
		}
		if !validKind(meta.Kind) {
			problems = append(problems, fmt.Errorf("%w: %s: %q", ErrActionMetaKind, command, meta.Kind))
			continue
		}
		if meta.Icon == "" {
			problems = append(problems, fmt.Errorf("%w: %s", ErrActionMetaIcon, command))
			continue
		}
		m.commands[command] = meta
	}
	return m, errors.Join(problems...)
}

// Missing lists the commands the game serves to players that have no line,
// sorted. The shipped table must have none.
func (m *ActionMetadata) Missing() []string {
	var out []string
	for _, s := range commands.All() {
		if s.Origin != commands.FromPlayer {
			continue
		}
		if m == nil {
			out = append(out, s.Command())
			continue
		}
		if _, ok := m.commands[s.Command()]; !ok {
			out = append(out, s.Command())
		}
	}
	sort.Strings(out)
	return out
}

// Of returns command's action metadata, falling back to the file's defaults
// for a command the table does not mention.
func (m *ActionMetadata) Of(command string) ActionMeta {
	if m == nil {
		return ActionMeta{Kind: KindSecondary, Icon: "action:default"}
	}
	if a, ok := m.commands[command]; ok {
		return a
	}
	return m.defaults
}
