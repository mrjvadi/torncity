package settlement

import "testing"

func testRules() FormRules {
	return FormRules{
		NameMin: 3, NameMax: 24, MottoMax: 20,
		CurrencyNameMin: 3, CurrencyNameMax: 24, CurrencyCodeLen: 3, CurrencySymbolMax: 3,
		Shapes:        []Choice{{"shield", "🛡"}, {"circle", "⭕"}},
		Icons:         []Choice{{"wheat", "🌾"}, {"tower", "🏰"}},
		Palette:       []Choice{{"red", "🔴"}, {"gold", "🟡"}, {"blue", "🔵"}},
		BannedWords:   []string{"badword", "foulmouthed"},
		ReservedNames: []string{"Support", "ساپورت"},
		ReservedCodes: []string{"SUP", "NIL", "USD"},
	}
}

func goodForm() Form {
	return Form{
		Name: "کورندال", Motto: "با هم", CurrencyName: "سکه کورندال", CurrencyCode: "krd", CurrencySymbol: "ک",
		Emblem: Emblem{Shape: "shield", ColorA: "red", ColorB: "gold", Icon: "wheat"},
	}
}

func codesOf(p []Problem) map[string]bool {
	m := map[string]bool{}
	for _, x := range p {
		m[x.Field+":"+x.Code] = true
	}
	return m
}

func TestCheckAcceptsAGoodFormAndNormalisesIt(t *testing.T) {
	got, problems := testRules().Check(goodForm())
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if got.CurrencyCode != "KRD" {
		t.Errorf("code = %q, want it upper-cased", got.CurrencyCode)
	}
	f := goodForm()
	f.CurrencySymbol = ""
	got, problems = testRules().Check(f)
	if len(problems) != 0 || got.CurrencySymbol != "KRD" {
		t.Errorf("an empty symbol should default to the code: %q %v", got.CurrencySymbol, problems)
	}
}

func TestCheckNames(t *testing.T) {
	r := testRules()
	cases := []struct {
		name string
		code string
	}{
		{"ab", ProblemNameShort},
		{"abcdefghijklmnopqrstuvwxyz", ProblemNameLong},
		{"Village7", ProblemNameChars},
		{"روستای ۱۲", ProblemNameChars},
		{"t.me/joinme", ProblemNameLink},
		{"@village", ProblemNameLink},
		{"visit www site", ProblemNameLink},
		{"support", ProblemNameReserved},
		{"سا‌پورت", ProblemNameReserved},
		{"the badword town", ProblemNameForbidden},
		{"foulmouthedville", ProblemNameForbidden},
	}
	for _, c := range cases {
		f := goodForm()
		f.Name = c.name
		_, problems := r.Check(f)
		if !codesOf(problems)["name:"+c.code] {
			t.Errorf("name %q: problems = %v, want %s", c.name, problems, c.code)
		}
	}
	for _, ok := range []string{"Aria", "کورندال", "می‌شود", "Port-Royal", "دهکده سبز", "ي ک"} {
		f := goodForm()
		f.Name = ok
		if _, problems := r.Check(f); name(problems) {
			t.Errorf("name %q refused: %v", ok, problems)
		}
	}
}

func name(p []Problem) bool {
	for _, x := range p {
		if x.Field == FieldName {
			return true
		}
	}
	return false
}

func TestNormaliseAndNameKey(t *testing.T) {
	if got := Normalize("  ك   ي‏ "); got != "ک ی" {
		t.Errorf("Normalize = %q", got)
	}
	if NameKey("Ab  Cd") != NameKey("ab-cd") || NameKey("ab-cd") != NameKey("ABCD") {
		t.Error("NameKey should ignore case, spaces and hyphens")
	}
	if NameKey("می‌شود") != NameKey("میشود") {
		t.Error("NameKey should ignore the non-joiner")
	}
}

func TestCheckMottoCurrencyAndEmblem(t *testing.T) {
	r := testRules()
	f := goodForm()
	f.Motto = "visit example.com now"
	f.CurrencyCode = "SUP"
	f.CurrencyName = "x"
	f.CurrencySymbol = "a b"
	f.Emblem.ColorB = f.Emblem.ColorA
	got := codesOf(mustProblems(r, f))
	for _, want := range []string{"motto:" + ProblemMottoLink, "currency_code:" + ProblemCurrencyCodeRes,
		"currency_name:" + ProblemCurrencyNameShort, "currency_symbol:" + ProblemCurrencySymbol, "emblem:" + ProblemEmblem} {
		if !got[want] {
			t.Errorf("missing %s in %v", want, got)
		}
	}

	f = goodForm()
	f.CurrencyCode = "AB1"
	f.Motto = "this motto is far too long to fit"
	f.Emblem.Icon = "nope"
	got = codesOf(mustProblems(r, f))
	for _, want := range []string{"currency_code:" + ProblemCurrencyCode, "motto:" + ProblemMottoLong, "emblem:" + ProblemEmblem} {
		if !got[want] {
			t.Errorf("missing %s in %v", want, got)
		}
	}
}

func mustProblems(r FormRules, f Form) []Problem {
	_, p := r.Check(f)
	return p
}

func TestEmblemText(t *testing.T) {
	if got := testRules().EmblemText(goodForm().Emblem); got != "🛡 🌾 🔴🟡" {
		t.Errorf("EmblemText = %q", got)
	}
}
