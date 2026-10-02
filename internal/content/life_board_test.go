package content

import "testing"

func TestBoardNamesHideHandlesAndTheGamesOwnAccounts(t *testing.T) {
	lb := LeaderboardsDef{HiddenNames: []string{"Nil", "NIL Guard Agent"}}
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"arad @MajorChats", "arad", true},
		{"@MajorChats", "", true},
		{"Sara", "Sara", true},
		{"nil", "", false},
		{"NIL guard agent", "", false},
		{"  Kaveh  ", "Kaveh", true},
	} {
		got, ok := lb.PublicName(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("PublicName(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
