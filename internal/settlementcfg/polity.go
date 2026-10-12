package settlementcfg

import (
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/polity"
)

// Polity turns the polity section of the config into the rules the handlers take (docs/adr/0076). A rule date that does not parse leaves
// the unions, the treaties of settlements, the tenders and the loans between polities shut.
func Polity(c config.Polity) application.PolityRules {
	at, _ := time.Parse(time.RFC3339, c.RuleAt)
	return application.PolityRules{
		RuleAt: at, RatifyDays: c.RatifyDays, ExitNoticeDays: c.ExitNoticeDays, CountryMinResidents: c.CountryMinResidents,
		LeagueFee: c.LeagueFee, CountryFee: c.CountryFee, MaxDuesBPS: c.MaxDuesBPS, WeightCapBPS: c.WeightCapBPS,
		MembersMajorityBPS: c.MembersMajorityBPS, PopulationMajorityBPS: c.PopulationMajorityBPS,
		VoteWindow: time.Duration(c.VoteWindowHours) * time.Hour, ExpelAfterPeriods: c.ExpelAfterPeriods, DuesPeriod: time.Duration(c.DuesPeriodHours) * time.Hour,
		TreatyCooling: time.Duration(c.TreatyCoolingDays) * 24 * time.Hour,
		TenderFee: c.TenderFee, BondBPS: c.BondBPS, RetentionBPS: c.RetentionBPS, DefectsDays: c.DefectsDays, DebarDays: c.DebarDays, CoiRefuseFromBPS: c.CoiRefuseFromBPS,
		TenderMin: time.Duration(c.TenderMinHours) * time.Hour, TenderGrace: time.Duration(c.TenderGraceHours) * time.Hour,
		DebtDefaultAfter: c.DebtDefaultAfter, DebtLateFeeBPS: c.DebtLateFeeBPS, DebtServiceCapBPS: c.DebtServiceCapBPS, DebtMaxInstalments: c.DebtMaxInstalments,
		DebtPeriod: time.Duration(c.DebtPeriodHours) * time.Hour, DebtMaxRateBPS: c.DebtMaxRateBPS,
		RequiredDelegations: []polity.Class{polity.War, polity.Treaty, polity.Courts},
		// a member's assembly also decides when the dues a proposal sets are a tenth of the income or more (docs/adr/0076 4.2)
		AssemblyDuesBoundBPS: c.MaxDuesBPS / 2,
	}
}
