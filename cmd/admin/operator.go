package main

import (
	"flag"
	"fmt"
	"strings"
)

// Every command that changes the game writes an audit row, and an audit row
// is only as useful as its answer to "who did this". The operator is named on
// the command line with --by, or once per shell or container with
// TORN_OPERATOR; an interactive login's $USER is the last resort. When none
// of them names anybody the command is refused before it touches anything,
// rather than recording a change made by "unknown".
//
// --actor is the flag's earlier name. It is kept so the scripts and runbooks
// that already pass it keep working; it means exactly what --by means.

// operatorEnv is the variable that names the operator for every command run
// from one shell or one container, for example set by docker-compose.yml from
// the host's login.
const operatorEnv = "TORN_OPERATOR"

// operatorFlags are --by and its older spelling --actor.
type operatorFlags struct {
	by, actor *string
}

// addOperatorFlags registers --by and --actor on a mutating command.
func addOperatorFlags(fs *flag.FlagSet) operatorFlags {
	return operatorFlags{
		by:    fs.String("by", "", "who is running this, recorded in the audit row (defaults to $"+operatorEnv+", then $USER)"),
		actor: fs.String("actor", "", "the same as --by"),
	}
}

// resolve returns the operator to record for command, or an error naming the
// ways to supply one. lookup reads the environment; os.LookupEnv in
// production.
func (o operatorFlags) resolve(command string, lookup func(string) (string, bool)) (string, error) {
	by, actor := strings.TrimSpace(*o.by), strings.TrimSpace(*o.actor)
	switch {
	case by != "" && actor != "" && by != actor:
		return "", fmt.Errorf("%s: --by %q and --actor %q name two operators; pass one", command, by, actor)
	case by != "":
		return by, nil
	case actor != "":
		return actor, nil
	}

	// An empty variable is treated as unset: docker-compose.yml passes
	// TORN_OPERATOR through even when the host has nothing to give it.
	for _, name := range []string{operatorEnv, "USER"} {
		if v, ok := lookup(name); ok {
			if v = strings.TrimSpace(v); v != "" {
				return v, nil
			}
		}
	}
	return "", fmt.Errorf("%s: nobody to record as the operator; pass --by NAME or set %s, "+
		"because a change whose author was not recorded cannot be accounted for later", command, operatorEnv)
}
