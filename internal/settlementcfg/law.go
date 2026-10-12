package settlementcfg

import (
	"reflect"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/law"
)

// Law turns the law section of the config into the rules the handlers take (docs/adr/0078). A rule date that does not parse leaves that part of the
// item shut. The tuning is copied field by field by name: every figure of config.Law that law.Tuning names reaches it, and the test proves none is left.
func Law(c config.Law) application.LawRules {
	at := func(s string) time.Time { t, _ := time.Parse(time.RFC3339, s); return t }
	r := application.LawRules{
		PoliceAt: at(c.PoliceRuleAt), CourtsAt: at(c.CourtsRuleAt), BlackAt: at(c.BlackRuleAt), CustomsAt: at(c.CustomsRuleAt), LaunderAt: at(c.LaunderRuleAt),
		Grace: time.Duration(c.GraceDays) * 24 * time.Hour,
	}
	src, dst := reflect.ValueOf(c), reflect.ValueOf(&r.T).Elem()
	for i := 0; i < dst.NumField(); i++ {
		if f := src.FieldByName(dst.Type().Field(i).Name); f.IsValid() && f.Kind() == reflect.Int64 {
			dst.Field(i).SetInt(f.Int())
		}
	}
	return r
}

// Compile-time proof the tuning type is the one the rules use.
var _ = law.Tuning{}
