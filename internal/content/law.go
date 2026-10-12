package content

import "fmt"

// Law and the underworld (docs/adr/0078). Two families of rows, each with the head every schema row carries (source, evidence, requires):
// offences.yml (the closed catalogue a charter may outlaw) and shadow_supply.yml (what the hidden board's NPC fences offer). Both are
// mechanics: an offence is a name, a class and bounds on what a court may hand down; a supply row is a good that already exists in the
// game, its daily quantity and who may see it. Nothing here describes how to do anything.

// The classes of an offence, from the lightest to the gravest.
const (
	OffencePetty   = "petty"
	OffenceSerious = "serious"
	OffenceGrave   = "grave"
)

var offenceClasses = map[string]bool{OffencePetty: true, OffenceSerious: true, OffenceGrave: true}

// The kinds of sentence a court may hand down.
const (
	SentenceFine        = "fine"
	SentenceJail        = "jail"
	SentenceBan         = "ban"
	SentenceRestitution = "restitution"
	SentenceForfeiture  = "forfeiture"
	SentenceCaution     = "caution"
)

var sentenceKinds = map[string]bool{SentenceFine: true, SentenceJail: true, SentenceBan: true, SentenceRestitution: true,
	SentenceForfeiture: true, SentenceCaution: true}

// SentenceKinds are the kinds a court may hand down, in a stable order.
func SentenceKinds() []string {
	return []string{SentenceCaution, SentenceFine, SentenceRestitution, SentenceBan, SentenceJail, SentenceForfeiture}
}

// OffenceDef is one offence of the closed catalogue (offences.yml). A settlement outlaws it by its charter (law.set); where it is not listed the
// act is not a crime there.
type OffenceDef struct {
	Head  `yaml:",inline"`
	Name  string `yaml:"name,omitempty" json:"name,omitempty"`
	Class string `yaml:"class" json:"class"`
	// Sentences are the kinds of sentence the offence may carry; MaxFine, MaxJailHours and MaxBanDays are the code bounds a charter and a judge
	// work inside (a charter may only lower them).
	Sentences    []string `yaml:"sentences" json:"sentences"`
	MaxFine      int64    `yaml:"max_fine,omitempty" json:"max_fine,omitempty"`
	MaxJailHours int64    `yaml:"max_jail_hours,omitempty" json:"max_jail_hours,omitempty"`
	MaxBanDays   int64    `yaml:"max_ban_days,omitempty" json:"max_ban_days,omitempty"`
	// Mature marks an offence that belongs to the mature content: not listed, not chargeable and not sent while the operator switch is off.
	Mature bool `yaml:"mature,omitempty" json:"mature,omitempty"`
}

// Allows says the offence may carry a kind of sentence.
func (d OffenceDef) Allows(kind string) bool {
	for _, s := range d.Sentences {
		if s == kind {
			return true
		}
	}
	return false
}

// ShadowSupplyDef is one good the hidden board's NPC fences offer each day (shadow_supply.yml). The good already exists in items.yml; only a
// settlement that has what the row requires has the supply, and only a player with enough standing sees the board at all.
type ShadowSupplyDef struct {
	Head `yaml:",inline"`
	Item string `yaml:"item" json:"item"`
	// Daily is how many the fences put up each local day; PremiumBPS is added to the item's base price, the price of having no legal source.
	Daily      int64 `yaml:"daily" json:"daily"`
	PremiumBPS int64 `yaml:"premium_bps" json:"premium_bps"`
	// MinStanding is the underworld standing a buyer needs.
	MinStanding int64 `yaml:"min_standing,omitempty" json:"min_standing,omitempty"`
	// Offence is the offence a trade in the good is, where a settlement outlaws it (offences.yml); "" for a good whose trade is no offence.
	Offence string `yaml:"offence,omitempty" json:"offence,omitempty"`
	Mature  bool   `yaml:"mature,omitempty" json:"mature,omitempty"`
}

// law checks the two families (called from the building schema's lint).
func (l *schemaLint) law() {
	oseen := map[string]bool{}
	for _, d := range l.p.Offences {
		key := l.head("offence", d.Head, oseen)
		if !wordCode.MatchString(d.Code) {
			l.bad("%s: the code is a plain word", key)
		}
		if !offenceClasses[d.Class] {
			l.bad("%s: class %q is not petty, serious or grave", key, d.Class)
		}
		if len(d.Sentences) == 0 {
			l.bad("%s: an offence names the sentences it may carry", key)
		}
		for _, s := range d.Sentences {
			if !sentenceKinds[s] {
				l.bad("%s: sentence %q is not one of the kinds a court may hand down", key, s)
			}
		}
		if d.Allows(SentenceFine) && d.MaxFine <= 0 || d.Allows(SentenceJail) && d.MaxJailHours <= 0 || d.Allows(SentenceBan) && d.MaxBanDays <= 0 {
			l.bad("%s: a sentence the offence allows has a bound above zero", key)
		}
		l.needs(key, d.Requires)
	}
	known := map[string]bool{}
	for _, o := range l.p.Offences {
		known[o.Code] = true
	}
	sseen := map[string]bool{}
	for _, d := range l.p.ShadowSupply {
		key := l.head("shadow_supply", d.Head, sseen)
		if !l.itemKnown(d.Item) {
			l.bad("%s: item %q does not exist", key, d.Item)
		}
		if d.Daily < 1 || d.PremiumBPS < 0 || d.MinStanding < 0 {
			l.bad("%s: daily is at least 1 and the premium and the standing are not negative", key)
		}
		for _, it := range l.p.Items {
			if it.Code == d.Item && !it.BlackMarket {
				l.bad("%s: %s has a legal source; only a good marked black_market is sold by the fences", key, d.Item)
			}
		}
		if d.Offence != "" && !known[d.Offence] {
			l.bad("%s: offence %q is not in offences.yml", key, d.Offence)
		}
		l.needs(key, d.Requires)
	}
}

// Offences lists the catalogue in file order.
func (s *Snapshot) Offences() []OffenceDef {
	if s.schema == nil {
		return nil
	}
	return append([]OffenceDef(nil), s.schema.offences...)
}

// Offence is an offence by code.
func (s *Snapshot) Offence(code string) (OffenceDef, bool) {
	for _, d := range s.Offences() {
		if d.Code == code {
			return d, true
		}
	}
	return OffenceDef{}, false
}

// ShadowSupply lists what the fences offer in file order.
func (s *Snapshot) ShadowSupply() []ShadowSupplyDef {
	if s.schema == nil {
		return nil
	}
	return append([]ShadowSupplyDef(nil), s.schema.shadow...)
}

// ShadowSupplyOf is the supply row of an item.
func (s *Snapshot) ShadowSupplyOf(item string) (ShadowSupplyDef, bool) {
	for _, d := range s.ShadowSupply() {
		if d.Item == item {
			return d, true
		}
	}
	return ShadowSupplyDef{}, false
}

func (d OffenceDef) String() string { return fmt.Sprintf("offence/%s", d.Code) }
