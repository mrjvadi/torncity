package main

import (
	"flag"
	"io"
	"strings"
	"testing"
)

func parseOperator(t *testing.T, args ...string) operatorFlags {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	o := addOperatorFlags(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return o
}

func env(vars map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

func TestOperatorIsResolvedInOrder(t *testing.T) {
	both := map[string]string{operatorEnv: "from-env", "USER": "from-login"}
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"--by wins over the environment", []string{"--by", "ada"}, both, "ada"},
		{"--actor is the same flag", []string{"--actor", "ada"}, both, "ada"},
		{"both flags naming the same operator", []string{"--by", "ada", "--actor", "ada"}, both, "ada"},
		{"the flag is trimmed", []string{"--by", "  ada "}, both, "ada"},
		{"TORN_OPERATOR before the login", nil, both, "from-env"},
		{"the login when nothing else names anyone", nil, map[string]string{"USER": "from-login"}, "from-login"},
		{"an empty TORN_OPERATOR is unset", nil, map[string]string{operatorEnv: " ", "USER": "from-login"}, "from-login"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseOperator(t, tc.args...).resolve("content load", env(tc.env))
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got != tc.want {
				t.Errorf("operator = %q, want %q", got, tc.want)
			}
		})
	}
}

// "unknown" is never recorded: with nobody named, the command is refused and
// the error says how to name someone.
func TestNoOperatorIsRefused(t *testing.T) {
	for name, vars := range map[string]map[string]string{
		"nothing set":    {},
		"all set empty":  {operatorEnv: "", "USER": ""},
		"only blank":     {operatorEnv: "  ", "USER": "\t"},
		"unrelated vars": {"LOGNAME": "ada", "HOME": "/home/ada"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := parseOperator(t, "--by", " ").resolve("office appoint", env(vars))
			if err == nil {
				t.Fatalf("resolve = %q, want a refusal", got)
			}
			for _, want := range []string{"office appoint", "--by", operatorEnv} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

func TestTwoDifferentOperatorsAreRefused(t *testing.T) {
	if got, err := parseOperator(t, "--by", "ada", "--actor", "bob").resolve("economy grant-starting", env(nil)); err == nil {
		t.Fatalf("resolve = %q, want a refusal", got)
	}
}
