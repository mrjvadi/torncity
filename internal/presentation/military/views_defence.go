package military

import (
	"time"

	"github.com/mrjvadi/torncity/internal/presentation/economy"
)

// Callback addresses of the defence licences.
const (
	AddrCompanyDefence = "company:defence"
	AddrLicences       = "military:licences"
	AddrLicence        = "military:licence"
)

// The minister's verdicts on a licence.
const (
	LicenceApprove = "approve"
	LicenceReject  = "reject"
	LicenceRevoke  = "revoke"
)

// CompanyRefusedDefence refuses founding a company of the defence sector
// without a defence licence.
const CompanyRefusedDefence = "defence"

// LicenceEntry is one licence or application as the screens show it.
type LicenceEntry struct {
	No      int64
	Company CompanyRef
	// Kind is manufacturer or contractor; Basis what it rests on; Status
	// where it stands (as settled now: a revocation past its notice is
	// revoked).
	Kind   string
	Basis  string
	Status string
	// EffectiveAt is when a revocation takes effect.
	EffectiveAt time.Time
}

// CompanyDefenceView is a company's defence licence screen.
type CompanyDefenceView struct {
	Ref CompanyRef
	// Licence is its latest licence or application, nil for none.
	Licence *LicenceEntry
	// Manufacturer is a company of the defence sector; otherwise it is a
	// civilian company that may become a contractor.
	Manufacturer bool
	// Owned and Tier are its standing in technology: technologies of its
	// own, and the deepest tier among them; MinTechs and MinTier what an
	// application asks.
	Owned, Tier       int
	MinTechs, MinTier int
	// CanApply is an owner whose company may apply now.
	CanApply bool
	// Applied is set just after the application went in.
	Applied bool
	// NoMinister says nobody holds or acts for the defence minister's
	// seat: an application waits.
	NoMinister bool
}

// LicencesView is a country's public registry of defence licences.
type LicencesView struct {
	// Unavailable, when set, is the whole answer: the service is not offered in
	// the settlement the viewer stands in, and no other fact is carried.
	Unavailable *economy.Unavailable
	Country GovPlace
	Pending []LicenceEntry
	InForce []LicenceEntry
	Ended   []LicenceEntry
	// CanDecide is the viewer who decides for the defence minister, in
	// their private chat.
	CanDecide bool
	// Notice is the verdict the minister just gave (approve, reject,
	// revoke) on NoticeCompany.
	Notice        string
	NoticeCompany string
	// Confirm is a licence the minister is about to revoke, and Notice
	// the notice it will run.
	Confirm      *LicenceEntry
	RevokeNotice time.Duration
}

// LicenceNoticeView is a private notice about a licence.
type LicenceNoticeView struct {
	// Kind is applied (to the minister), approved, rejected or revoked
	// (to the owner).
	Kind    string
	Company CompanyRef
	Country GovPlace
	// EffectiveAt is when a revocation takes effect.
	EffectiveAt time.Time
}

