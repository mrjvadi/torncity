package groups

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mrjvadi/torncity/internal/commands"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Where each command may run, and whose eyes its reply is for.
//
// The owner's rule is that a group is a public square with a few things open
// in it, and everything else happens in the player's private chat with the
// bot. Which command is which is DATA, in configs/commands.yml, one line per
// command:
//
//	crime.commit:  {channel: group}
//	bank.show:     {channel: private}
//	bank.pay:      {channel: both, reply: private}
//
// channel is where the command may be RUN:
//
//   - group: only in a group. Sent in the private chat, the gateway answers
//     with a short hint that it is done in a group, and runs nothing.
//   - private: only in the private chat. Sent in a group, the gateway answers
//     with a short hint and a button that opens the private chat and runs it
//     there (a deep link), and runs nothing in the group.
//   - both: anywhere.
//
// reply says whose eyes the reply is for when a "both" command is run in a
// group: public (the default) posts it in the group; private sends it to the
// player's private chat and leaves one neutral line in the group (see
// Renderer.direct). A screen can also mark itself private
// (presenter.Response.Private) whatever its command's reply says, which is
// how a public screen keeps one player's exact money out of the room.
//
// Every command the game serves to players must have a line: a test loads the
// shipped file and holds it to internal/commands, so a new command cannot
// ship without somebody deciding where it belongs.

// Channel is where a command may run.
type Channel string

// The three channels.
const (
	ChannelGroup   Channel = "group"
	ChannelPrivate Channel = "private"
	ChannelBoth    Channel = "both"
)

// Reply visibility for a command run in a group.
const (
	ReplyPublic  = "public"
	ReplyPrivate = "private"
)

// Rule is one command's line in configs/commands.yml.
type Rule struct {
	Channel Channel `yaml:"channel"`
	Reply   string  `yaml:"reply"`
}

// InputSpec says how a pending free-text input (internal/gateway/input) fills
// one command: which payload field the typed text goes into, and the names of
// the arguments the button that asked for it carries, in order.
type InputSpec struct {
	Field string   `yaml:"field"`
	Args  []string `yaml:"args"`
}

// Policy is configs/commands.yml, loaded. The zero value and nil allow every
// command everywhere and keep every reply public, except that the bank's and
// the settings' replies stay private, the rule the game shipped with before
// the table existed.
type Policy struct {
	rules map[string]Rule
	input map[string]InputSpec
}

// policyFile mirrors configs/commands.yml.
type policyFile struct {
	Commands map[string]Rule      `yaml:"commands"`
	Input    map[string]InputSpec `yaml:"input"`
}

// Failures LoadPolicy reports.
var (
	ErrPolicySyntax  = errors.New("groups: commands.yml is not valid")
	ErrPolicyChannel = errors.New("groups: a command names no valid channel")
	ErrPolicyCommand = errors.New("groups: a line names no command the game serves")
)

// LoadPolicy reads the command table.
func LoadPolicy(path string) (*Policy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("groups: read %s: %w", path, err)
	}
	return ParsePolicy(data)
}

// ParsePolicy reads the command table from its yaml. Every line must name a
// command the game serves to players and a valid channel and reply; the
// first problem of each kind is reported with its command.
func ParsePolicy(data []byte) (*Policy, error) {
	var f policyFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPolicySyntax, err)
	}
	p := &Policy{rules: map[string]Rule{}, input: map[string]InputSpec{}}
	var problems []error
	for command, rule := range f.Commands {
		if !commands.FromPlayerCommand(command) {
			problems = append(problems, fmt.Errorf("%w: %q", ErrPolicyCommand, command))
			continue
		}
		switch rule.Channel {
		case ChannelGroup, ChannelPrivate, ChannelBoth:
		default:
			problems = append(problems, fmt.Errorf("%w: %s: %q", ErrPolicyChannel, command, rule.Channel))
			continue
		}
		switch rule.Reply {
		case "", ReplyPublic, ReplyPrivate:
		default:
			problems = append(problems, fmt.Errorf("%w: %s: reply %q", ErrPolicyChannel, command, rule.Reply))
			continue
		}
		p.rules[command] = rule
	}
	for command, spec := range f.Input {
		if !commands.FromPlayerCommand(command) || strings.TrimSpace(spec.Field) == "" {
			problems = append(problems, fmt.Errorf("%w: input %q", ErrPolicyCommand, command))
			continue
		}
		p.input[command] = spec
	}
	return p, errors.Join(problems...)
}

// Missing lists the commands the game serves to players that have no line,
// sorted. The shipped table must have none.
func (p *Policy) Missing() []string {
	var out []string
	for _, s := range commands.All() {
		if s.Origin != commands.FromPlayer {
			continue
		}
		if p == nil {
			out = append(out, s.Command())
			continue
		}
		if _, ok := p.rules[s.Command()]; !ok {
			out = append(out, s.Command())
		}
	}
	sort.Strings(out)
	return out
}

// Channel is where command may run. A command with no line may run anywhere:
// the table is enforced by its test, and a gap in it must not lock a player
// out.
func (p *Policy) Channel(command string) Channel {
	if p == nil {
		return ChannelBoth
	}
	if r, ok := p.rules[command]; ok {
		return r.Channel
	}
	return ChannelBoth
}

// Allowed reports whether command may run in a chat of this kind.
func (p *Policy) Allowed(command string, inGroup bool) bool {
	switch p.Channel(command) {
	case ChannelGroup:
		return inGroup
	case ChannelPrivate:
		return !inGroup
	}
	return true
}

// Input is how a pending free-text input fills command, and whether one may.
func (p *Policy) Input(command string) (InputSpec, bool) {
	if p == nil {
		return InputSpec{}, false
	}
	spec, ok := p.input[command]
	return spec, ok
}

// legacyPrivate is what a nil or empty policy keeps private: the bank and the
// settings, which were private before the table existed.
var legacyPrivate = map[string]bool{
	"bank":                true,
	"player.settings":     true,
	"player.language.set": true,
}

// IsPrivate reports whether a response to command must stay out of a group:
// the screen says so, or the command runs only in private chats, or its line
// says its reply is private.
func (p *Policy) IsPrivate(command string, resp *presenter.Response) bool {
	if resp != nil && resp.Private {
		return true
	}
	if p == nil || len(p.rules) == 0 {
		if legacyPrivate[command] {
			return true
		}
		domain, _, _ := strings.Cut(command, ".")
		return legacyPrivate[domain]
	}
	r, ok := p.rules[command]
	if !ok {
		return false
	}
	return r.Channel == ChannelPrivate || r.Reply == ReplyPrivate
}

// IsPrivate is Policy.IsPrivate for the table the game shipped with before
// configs/commands.yml, for callers that hold no policy.
func IsPrivate(command string, resp *presenter.Response) bool {
	return (*Policy)(nil).IsPrivate(command, resp)
}
