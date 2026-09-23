// Package screentest renders responses as a player reads them and checks
// what reaches a player, for the snapshot tests of the screens and of the
// handlers behind them.
//
// # Golden files
//
// A snapshot test renders screens with realistic values into text files under
// testdata/snapshots/<language>/. Those files are the review artefact: anyone
// can read exactly what a player is shown, in both languages, without running
// the game — and a change to a screen or to a locale shows up as a diff of
// what players will read. Regenerate them with
//
//	go test ./internal/telegram/screens ./internal/application/handlers -run Snapshot -update
//
// # Lint
//
// Problems lists everything on a screen that must never reach a player: a
// Go "nil", a formatting error, an unfilled placeholder, an identifier, a
// catalogue key or content code, ASCII digits or Latin words inside Persian
// text, and blank values. Every snapshot is linted, so a bug of that kind
// fails the build the moment a screen starts showing it.
package screentest

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Update rewrites the golden files instead of comparing against them. It is
// registered here once so every package using this one accepts -update.
var Update = flag.Bool("update", false, "rewrite the screen snapshot golden files")

// Transcript renders a response the way it reaches a chat: whether it sends
// or edits, its text, and each row of buttons with the address behind every
// button.
func Transcript(resp *presenter.Response) string {
	if resp == nil {
		return "(no response)\n"
	}
	var b strings.Builder
	switch resp.Type {
	case presenter.ActionEditMessage:
		fmt.Fprintf(&b, "(edits message %d)", resp.MessageID)
	case presenter.ActionAnswerCallback:
		b.WriteString("(popup)")
	default:
		b.WriteString("(sends)")
	}
	if resp.Private {
		b.WriteString(" (private in groups)")
	}
	b.WriteString("\n")
	b.WriteString(resp.Text)
	b.WriteString("\n")
	if resp.Keyboard != nil && len(resp.Keyboard.Rows) > 0 {
		b.WriteString("  ───\n")
		for _, row := range resp.Keyboard.Rows {
			cells := make([]string, 0, len(row))
			for _, btn := range row {
				target := btn.CallbackData
				if btn.URL != "" {
					target = btn.URL
				}
				cells = append(cells, "["+btn.Text+"] → "+target)
			}
			b.WriteString("  " + strings.Join(cells, "   │   ") + "\n")
		}
	}
	return b.String()
}

// Visible is everything a player reads on a response: the text and every
// button label. Addresses are not visible and are not in it.
func Visible(resp *presenter.Response) []string {
	if resp == nil {
		return nil
	}
	out := []string{resp.Text}
	if resp.Keyboard != nil {
		for _, row := range resp.Keyboard.Rows {
			for _, btn := range row {
				out = append(out, btn.Text)
			}
		}
	}
	return out
}

// Book collects the screens of one area, in one language, into one golden
// file.
type Book struct {
	lang     string
	sections []string
	problems []string
	allowed  []string
}

// NewBook starts a golden file for lang. allowed are words the lint accepts
// in this language although they break its rules — the sample data's player
// names, a language's own name — and nothing else.
func NewBook(lang string, allowed ...string) *Book {
	return &Book{lang: lang, allowed: allowed}
}

// Add renders one screen under a heading and lints what a player sees.
func (b *Book) Add(title string, resp *presenter.Response) {
	b.sections = append(b.sections, "━━━ "+title+" ━━━\n"+Transcript(resp))
	for _, text := range Visible(resp) {
		for _, p := range Problems(b.lang, text, b.allowed...) {
			b.problems = append(b.problems, title+": "+p)
		}
	}
	if resp != nil && resp.Keyboard == nil && resp.Type != presenter.ActionAnswerCallback {
		b.problems = append(b.problems, title+": the screen has no buttons, so it is a dead end")
	}
}

// AddText records a line of text that is not a whole screen, such as a
// popup or a line another system posts.
func (b *Book) AddText(title, text string) {
	b.sections = append(b.sections, "━━━ "+title+" ━━━\n"+text+"\n")
	for _, p := range Problems(b.lang, text, b.allowed...) {
		b.problems = append(b.problems, title+": "+p)
	}
}

// String is the golden file's content.
func (b *Book) String() string { return strings.Join(b.sections, "\n") }

// Check fails t for every lint problem, then compares the book with its
// golden file at dir/<lang>/<name>.txt, or rewrites the file under -update.
func (b *Book) Check(t *testing.T, dir, name string) {
	t.Helper()
	for _, p := range b.problems {
		t.Errorf("[%s] %s", b.lang, p)
	}
	Golden(t, filepath.Join(dir, b.lang, name+".txt"), b.String())
}

// Golden compares got with the file at path, or writes it under -update.
func Golden(t *testing.T, path, got string) {
	t.Helper()
	if *Update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (run the test with -update to create it)", path, err)
	}
	if !bytes.Equal(want, []byte(got)) {
		t.Errorf("%s is out of date: what players see has changed.\n"+
			"Review the change, then regenerate with -update.\n%s", path, firstDifference(string(want), got))
	}
}

// firstDifference shows the first line that differs, which is usually enough
// to see what changed without dumping two whole files.
func firstDifference(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return fmt.Sprintf("line %d\n  golden: %q\n  now:    %q", i+1, wl, gl)
		}
	}
	return ""
}

var (
	goNil        = regexp.MustCompile(`<nil>|\bnil\b|%!|\(MISSING\)|\bNaN\b|[+-]Inf\b|0001-01-01`)
	placeholder  = regexp.MustCompile(`\{[A-Za-z0-9_.-]*\}`)
	uuid         = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	catalogueKey = regexp.MustCompile(`\b[a-z][a-z_]*\.[a-z][a-z_]*(\.[a-z_]+)*\b`)
	contentCode  = regexp.MustCompile(`\b[a-z]+_[a-z_]+\b`)
	// command is a command a player types, with its arguments: it is written
	// the way it must be typed, in ASCII, in every language.
	command = regexp.MustCompile(`/[a-z_]+(?: +[@A-Za-z0-9_]+)*`)
	// playerCode is a public player code, which is ASCII by design.
	playerCode = regexp.MustCompile(`\b[0-9A-Z]{7}\b`)
	// username is a Telegram username echoed back, which is the player's own
	// spelling.
	username    = regexp.MustCompile(`@[A-Za-z0-9_]+`)
	latinWord   = regexp.MustCompile(`[A-Za-z]{2,}`)
	asciiDigits = regexp.MustCompile(`[0-9]+`)
	blankValue  = regexp.MustCompile(`: *$|\(\s*\)|« *»|“ *”|  \S| $`)
	bareBullet  = regexp.MustCompile(`^\s*•\s*$`)
)

// Problems lists what is wrong with one piece of visible text in lang.
// allowed words are exempt from the language checks.
func Problems(lang, text string, allowed ...string) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	if m := goNil.FindString(text); m != "" {
		add("a Go value leaked: %q in %q", m, text)
	}
	if m := placeholder.FindString(text); m != "" {
		add("an unfilled placeholder %s in %q", m, text)
	}
	if m := uuid.FindString(text); m != "" {
		add("an identifier %s in %q", m, text)
	}
	if strings.TrimSpace(text) == "" {
		add("an empty text")
	}

	// What a player types stays ASCII and is not prose; take it out before
	// judging the prose.
	prose := command.ReplaceAllString(text, " ")
	prose = playerCode.ReplaceAllString(prose, " ")
	prose = username.ReplaceAllString(prose, " ")
	for _, w := range allowed {
		prose = strings.ReplaceAll(prose, w, " ")
	}
	if m := catalogueKey.FindString(prose); m != "" {
		add("a catalogue key or code %q in %q", m, text)
	}
	if m := contentCode.FindString(prose); m != "" {
		add("a content code %q in %q", m, text)
	}

	for _, line := range strings.Split(text, "\n") {
		if bareBullet.MatchString(line) {
			add("an empty list line in %q", text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// A heading ends in a colon ("Requirements:"); a label with its
		// value missing ends in a colon and a space, or in two spaces.
		if m := blankValue.FindString(strings.TrimLeft(line, " ")); m != "" && !isHeading(line) {
			add("a blank value (%q) in line %q", m, line)
		}
	}

	switch lang {
	case "fa":
		if m := latinWord.FindString(prose); m != "" {
			add("a Latin word %q in Persian text %q", m, text)
		}
		if m := asciiDigits.FindString(prose); m != "" {
			add("ASCII digits %q in Persian text %q", m, text)
		}
		if strings.Contains(prose, "%") {
			add("an ASCII percent sign in Persian text %q", text)
		}
	case "en":
		for _, r := range prose {
			if unicode.In(r, unicode.Arabic) {
				add("Arabic-script text in English %q", text)
				break
			}
		}
	}
	return out
}

// isHeading reports whether line is a heading that rightly ends in a colon:
// no space before the colon and nothing after it.
func isHeading(line string) bool {
	l := strings.TrimRight(line, " ")
	return strings.HasSuffix(l, ":") && !strings.HasSuffix(l, " :") && l == line
}
