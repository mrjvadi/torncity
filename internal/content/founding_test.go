package content

import (
	"errors"
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/settlement"
)

func TestShippedFoundingIsUsable(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Founding) != 1 {
		t.Fatalf("founding sections = %d, want 1", len(pack.Founding))
	}
	var problems []error
	pack.validateFounding(&problems)
	if len(problems) != 0 {
		t.Fatalf("shipped founding content: %v", problems)
	}
	rules := pack.Founding[0].Rules(settlement.FormRules{NameMin: 3, NameMax: 24, MottoMax: 60,
		CurrencyNameMin: 3, CurrencyNameMax: 24, CurrencyCodeLen: 3, CurrencySymbolMax: 3})
	_, ps := rules.Check(settlement.Form{Name: "Korendal", CurrencyName: "Korendal mark", CurrencyCode: "KRD",
		Emblem: settlement.Emblem{Shape: "shield", ColorA: "crimson", ColorB: "gold", Icon: "wheat"}})
	if len(ps) != 0 {
		t.Errorf("a good form is refused with the shipped lists: %v", ps)
	}
}

func TestFoundingValidationRefusesNonsense(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, spoil := range map[string]func(*FoundingDef){
		"duplicate colour": func(d *FoundingDef) { d.Palette[1].Code = d.Palette[0].Code },
		"bad hex":          func(d *FoundingDef) { d.Palette[0].Hex = "red" },
		"no icons":         func(d *FoundingDef) { d.Icons = nil },
		"free SUP":         func(d *FoundingDef) { d.ReservedCurrencyCodes = []string{"NIL"} },
		"empty emoji":      func(d *FoundingDef) { d.Shapes[0].Emoji = "" },
		"lower-case code":  func(d *FoundingDef) { d.ReservedCurrencyCodes = append(d.ReservedCurrencyCodes, "abc") },
	} {
		t.Run(name, func(t *testing.T) {
			p := *pack
			d := pack.Founding[0]
			d.Shapes = append([]FoundingChoiceDef(nil), d.Shapes...)
			d.Palette = append([]FoundingChoiceDef(nil), d.Palette...)
			d.ReservedCurrencyCodes = append([]string(nil), d.ReservedCurrencyCodes...)
			spoil(&d)
			p.Founding = []FoundingDef{d}
			var problems []error
			p.validateFounding(&problems)
			if len(problems) == 0 || !errors.Is(problems[0], ErrInvalidFoundingContent) {
				t.Fatalf("validation = %v, want a founding problem", problems)
			}
		})
	}
}
