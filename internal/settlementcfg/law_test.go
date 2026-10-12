package settlementcfg

import (
	"reflect"
	"testing"

	"github.com/mrjvadi/torncity/internal/config"
)

// Every figure of the tuning is filled from the config: none is left at zero by a name that does not match, and the rule dates are the owner's.
func TestLawRulesCarryEveryFigureAndTheOwnersDates(t *testing.T) {
	r := Law(config.Defaults().Law)
	v := reflect.ValueOf(r.T)
	for i := 0; i < v.NumField(); i++ {
		if v.Field(i).Int() == 0 {
			t.Errorf("law.Tuning.%s is zero: no config key reaches it", v.Type().Field(i).Name)
		}
	}
	for name, got := range map[string]string{
		"police": r.PoliceAt.Format("2006-01-02"), "courts": r.CourtsAt.Format("2006-01-02"), "black": r.BlackAt.Format("2006-01-02"),
		"customs": r.CustomsAt.Format("2006-01-02"), "launder": r.LaunderAt.Format("2006-01-02"),
	} {
		want := map[string]string{"police": "2026-10-26", "courts": "2026-11-02", "black": "2026-11-09", "customs": "2026-11-16", "launder": "2026-11-16"}[name]
		if got != want {
			t.Errorf("%s starts %s, the owner said %s", name, got, want)
		}
	}
}
