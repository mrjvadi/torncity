package content

import (
	"errors"
	"testing"
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
	d := pack.Founding[0]
	if len(d.Shapes) < 3 || len(d.Palette) < 6 || len(d.Icons) < 8 {
		t.Errorf("the emblem catalogue is thin: %d shapes, %d colours, %d icons", len(d.Shapes), len(d.Palette), len(d.Icons))
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
