package playercode

import (
	"bytes"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The alphabet is the whole point of the code being readable aloud: every
// look-alike pair is a lookup that fails for no reason a player can see.
func TestAlphabetHasNoLookAlikes(t *testing.T) {
	for _, bad := range "01OIL" {
		if strings.ContainsRune(Alphabet, bad) {
			t.Errorf("the alphabet contains the look-alike %q", bad)
		}
	}
	seen := map[rune]bool{}
	for _, r := range Alphabet {
		if seen[r] {
			t.Errorf("the alphabet lists %q twice, which biases the draw", r)
		}
		seen[r] = true
		if !(r >= '2' && r <= '9') && !(r >= 'A' && r <= 'Z') {
			t.Errorf("the alphabet contains %q, which is neither 2-9 nor an upper-case letter", r)
		}
	}
	if len(Alphabet) != 31 {
		t.Errorf("the alphabet has %d symbols, want 31 (2-9 and A-Z without I, L, O)", len(Alphabet))
	}
}

// Many draws: every code is the right length, uses only the alphabet, is never
// all digits, and they do not collide. 20,000 honest draws from 31^7 hold one
// duplicate pair with probability about 0.7%, so one is tolerated to keep the
// test from flaking; two or more means the source is not random.
func TestNewDrawsWellFormedDistinctCodes(t *testing.T) {
	const draws = 20000
	seen := make(map[string]bool, draws)
	dups := 0
	counts := map[byte]int{}
	for i := 0; i < draws; i++ {
		code, err := New()
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if len(code) != Length {
			t.Fatalf("code %q has length %d, want %d", code, len(code), Length)
		}
		if !Valid(code) {
			t.Fatalf("New produced %q, which Valid refuses", code)
		}
		if seen[code] {
			dups++
		}
		seen[code] = true
		for j := 0; j < len(code); j++ {
			counts[code[j]]++
		}
	}
	if dups > 1 {
		t.Errorf("%d duplicate codes in %d draws; the source is not random", dups, draws)
	}
	// Every symbol turns up: a draw that could never produce some symbol
	// would shrink the space and make codes easier to guess.
	for i := 0; i < len(Alphabet); i++ {
		if counts[Alphabet[i]] == 0 {
			t.Errorf("symbol %q never appeared in %d draws", Alphabet[i], draws)
		}
	}
}

// A scripted source pins the two rejection rules: bytes at or above 248 are
// skipped, and an all-digit code is thrown away and drawn again.
func TestDrawRejectsBiasedBytesAndAllDigitCodes(t *testing.T) {
	// First 16 bytes: 248..255 are skipped, then seven 0s -> "2222222", which
	// is all digits and must be refused. The next 16 bytes supply the code
	// that is kept: index 8 is 'A', index 0 is '2'.
	first := append(bytes.Repeat([]byte{255}, 8), bytes.Repeat([]byte{0}, 8)...)
	second := []byte{8, 0, 0, 0, 0, 0, 0, 248, 249, 0, 0, 0, 0, 0, 0, 0}
	source := bytes.NewReader(append(first, second...))

	code, err := Draw(source)
	if err != nil {
		t.Fatalf("Draw: %v", err)
	}
	if code != "A222222" {
		t.Errorf("Draw = %q, want %q", code, "A222222")
	}
}

func TestDrawGivesUpOnAConstantSource(t *testing.T) {
	// A source of nothing but zeros only ever yields "2222222".
	_, err := Draw(zeros{})
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("Draw from a constant source = %v, want ErrExhausted", err)
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

func TestValid(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"K7Q2M9A", true},
		{"ABCDEFG", true},
		{"2345678", false},  // all digits: that is a Telegram user id
		{"k7q2m9a", false},  // Valid does not normalise; Normalize first
		{"K7Q2M9", false},   // too short
		{"K7Q2M9AB", false}, // too long
		{"K7Q2M0A", false},  // 0 is a look-alike
		{"K7Q2MOA", false},  // O is a look-alike
		{"K7Q1M9A", false},  // 1 is a look-alike
		{"K7QIM9A", false},  // I is a look-alike
		{"K7QLM9A", false},  // L is a look-alike
		{"K7Q M9A", false},
		{"", false},
		{"کدبازیک", false},
	} {
		if got := Valid(tc.in); got != tc.want {
			t.Errorf("Valid(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	if got := Normalize("  k7q2m9a "); got != "K7Q2M9A" {
		t.Errorf("Normalize = %q, want K7Q2M9A", got)
	}
}

// The migration repeats the alphabet (in the draw) and the shape (in the
// CHECK constraint). Both copies are read from the file and compared with the
// Go definition, so a change to one without the other fails here rather than
// as a constraint violation on some player's first contact.
func TestMigrationAgreesWithTheGoRules(t *testing.T) {
	raw, err := os.ReadFile("../../../migrations/0007_player_public_code.up.sql")
	if err != nil {
		t.Fatalf("reading the migration: %v", err)
	}
	sql := string(raw)

	if !strings.Contains(sql, "alphabet CONSTANT text := '"+Alphabet+"'") {
		t.Errorf("the migration's draw does not use the alphabet %q", Alphabet)
	}
	if !strings.Contains(sql, "WHILE length(code) < 7 LOOP") || Length != 7 {
		t.Errorf("the migration's draw does not produce %d characters", Length)
	}
	if !strings.Contains(sql, "CONTINUE WHEN b >= 248") || rejectAbove != 248 {
		t.Errorf("the migration's draw does not reject the same bytes as the Go draw (%d)", rejectAbove)
	}
	if !strings.Contains(sql, "1 + b % 31") || len(Alphabet) != 31 {
		t.Errorf("the migration's draw does not index a %d-symbol alphabet", len(Alphabet))
	}

	check := regexp.MustCompile(`public_code ~ '(\^\[[^']+\]\{7\}\$)'`).FindStringSubmatch(sql)
	if check == nil {
		t.Fatal("the migration has no shape CHECK on public_code")
	}
	shape := regexp.MustCompile(check[1])
	for b := 0; b < 256; b++ {
		one := strings.Repeat(string(rune(b)), Length)
		inAlphabet := strings.IndexByte(Alphabet, byte(b)) >= 0 && b < 128
		if got := shape.MatchString(one); got != inAlphabet {
			t.Errorf("the CHECK's character class and the alphabet disagree on %q (check %v, alphabet %v)",
				rune(b), got, inAlphabet)
		}
	}
}
