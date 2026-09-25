package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Health and hospitals (docs/adr/0023-health-missions-factions.md): the
// hospital screen — a player's health, their stay, and who can treat them —
// a treatment's price and its result, a clinic's desk, the notices of an
// admission, a discharge and a treatment given, and an injury as another
// screen reports it.

// Addresses of the health screens.
const (
	AddrHospital    = "health:hospital"
	AddrTreat       = "health:treat"
	AddrClinicDesk  = "health:clinic"
	AddrClinicOpen  = "health:open"
	commandClinicPr = "health.price"
)

// TreatCity names the city hospital in a treatment's address.
const TreatCity = application.ProviderCity

// InjuryView is an injury as a screen reports it: what it took, where it
// left the player, and whether it put them in hospital.
type InjuryView struct {
	Damage, Health, Max int
	Hospital            bool
	EndsAt              time.Time
}

// injuryLines are an injury's lines on another screen: a crime's result, a
// shift's end, a strike's notice.
func (c Context) injuryLines(v *InjuryView) string {
	if v == nil {
		return ""
	}
	args := map[string]any{"damage": FormatNumber(c, int64(v.Damage)), "health": FormatNumber(c, int64(v.Health)),
		"max": FormatNumber(c, int64(v.Max))}
	if !v.Hospital {
		return c.T("health.injury.hurt", args)
	}
	return body(c.T("health.injury.hospital", args), clockLine(c, "health.discharge_at", v.EndsAt))
}

// TreatOption is one way to be treated: the city hospital or a clinic.
type TreatOption struct {
	// Provider is application.ProviderCity or ProviderClinic; Clinic names
	// the clinic.
	Provider string
	Clinic   CompanyRef
	Price    int64
	// Saves is the real time a treatment would take off the stay.
	Saves time.Duration
	// Doctor is the clinic's best medicine skill; Stock its units of
	// medicine; Open whether it takes patients.
	Doctor   int
	Stock    int64
	Open     bool
	CanTreat bool
}

func (o TreatOption) address() string {
	if o.Provider == application.ProviderClinic {
		return o.Clinic.Code
	}
	return TreatCity
}

// providerName names who treats.
func (c Context) providerName(o TreatOption) string {
	if o.Provider == application.ProviderClinic {
		return c.T("health.clinic_name", map[string]any{"name": o.Clinic.Name, "code": o.Clinic.Code})
	}
	return c.T("health.city_hospital", nil)
}

// HospitalView is the hospital screen.
type HospitalView struct {
	Health, Max int
	// FullIn is how long rest takes to bring health back in full, out of
	// hospital; zero when it is full or nothing comes back at rest.
	FullIn         time.Duration
	CityCode, City string
	InHospital     bool
	Cause          string
	Remaining      time.Duration
	EndsAt         time.Time
	// Treated says the stay was treated, by TreatedBy.
	Treated   bool
	TreatedBy TreatOption
	// CityHospital is the city hospital's offer, nil when there is none to
	// make (not in hospital, or treated).
	CityHospital *TreatOption
	Clinics      []TreatOption
}

// Hospital renders the hospital screen.
func Hospital(c Context, v HospitalView) *presenter.Response {
	kb := keyboards.New()
	healthLine := c.T("health.health_line", map[string]any{"health": FormatNumber(c, int64(v.Health)),
		"max": FormatNumber(c, int64(v.Max))})
	var state string
	switch {
	case v.InHospital:
		state = body(
			c.T("health.in_hospital", map[string]any{"city": c.CityName(v.CityCode, v.City)}),
			c.T("health.cause."+v.Cause, nil),
			c.T("health.remaining", map[string]any{"remaining": FormatDuration(c, v.Remaining)}),
			clockLine(c, "health.discharge_at", v.EndsAt),
		)
	case v.FullIn > 0:
		state = body(c.T("health.not_in_hospital", nil),
			c.T("health.rest_full_in", map[string]any{"duration": FormatDuration(c, v.FullIn)}))
	default:
		state = c.T("health.not_in_hospital", nil)
	}
	var care string
	switch {
	case v.Treated:
		care = c.T("health.treated_by", map[string]any{"provider": c.providerName(v.TreatedBy)})
	case v.InHospital:
		lines := []string{c.T("health.treat_title", nil)}
		options := make([]TreatOption, 0, len(v.Clinics)+1)
		if v.CityHospital != nil {
			options = append(options, *v.CityHospital)
		}
		options = append(options, v.Clinics...)
		for _, o := range options {
			lines = append(lines, c.treatLine(o))
			if !o.CanTreat && o.Provider == application.ProviderClinic {
				continue
			}
			label := c.T("health.button.treat", map[string]any{"provider": c.providerName(o),
				"price": FormatMoney(c, o.Price), "saves": FormatDuration(c, o.Saves)})
			if btn, ok := keyboards.Button(label, AddrTreat, o.address()); ok {
				kb.Row(btn)
			}
		}
		care = body(lines...)
	case len(v.Clinics) > 0:
		lines := []string{c.T("health.clinics_title", map[string]any{"city": c.CityName(v.CityCode, v.City)})}
		for _, o := range v.Clinics {
			lines = append(lines, c.clinicLine(o))
		}
		care = body(lines...)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrHospital}))
	return c.respond(paragraphs(c.T("health.title", nil), healthLine, state, care), kb.Build()).MarkPrivate()
}

// treatLine is one offer, for a patient.
func (c Context) treatLine(o TreatOption) string {
	args := map[string]any{"provider": c.providerName(o), "price": FormatMoney(c, o.Price),
		"saves": FormatDuration(c, o.Saves), "doctor": FormatNumber(c, int64(o.Doctor))}
	switch {
	case o.Provider == application.ProviderCity:
		return c.T("health.offer.city", args)
	case !o.Open:
		return c.T("health.offer.closed", args)
	case !o.CanTreat:
		return c.T("health.offer.no_medicine", args)
	}
	return c.T("health.offer.clinic", args)
}

// clinicLine is one clinic, for someone not in hospital.
func (c Context) clinicLine(o TreatOption) string {
	args := map[string]any{"provider": c.providerName(o), "price": FormatMoney(c, o.Price),
		"doctor": FormatNumber(c, int64(o.Doctor))}
	if !o.Open {
		return c.T("health.offer.closed", args)
	}
	return c.T("health.clinic_line", args)
}

// TreatConfirmView is a treatment's price, before it is paid.
type TreatConfirmView struct {
	Option TreatOption
	// Remaining is the stay left now; EndsAt when it would end after.
	Remaining time.Duration
	EndsAt    time.Time
	// Payment is how it can be paid; nil for a free treatment, which has a
	// plain confirm button.
	Payment *PaymentChoice
}

// TreatConfirm renders a treatment's price and the ways to pay it.
func TreatConfirm(c Context, v TreatConfirmView) *presenter.Response {
	text := body(
		c.T("health.confirm", map[string]any{"provider": c.providerName(v.Option), "price": FormatMoney(c, v.Option.Price),
			"saves": FormatDuration(c, v.Option.Saves), "remaining": FormatDuration(c, v.Remaining)}),
		clockLine(c, "health.discharge_new", v.EndsAt),
	)
	kb := keyboards.New()
	var pay string
	switch {
	case v.Payment == nil:
		if btn, ok := keyboards.Button(c.T("health.button.confirm_free", nil), AddrTreat, v.Option.address(), MethodFree); ok {
			kb.Row(btn)
		}
	case len(v.Payment.Usable) > 0:
		pay = body(c.T("health.pay_how", nil), c.paymentNote(*v.Payment))
		c.paymentButtons(kb, *v.Payment, func(m string) []string { return []string{AddrTreat, v.Option.address(), m} })
	default:
		pay = body(c.T("payment.cannot_afford", nil), c.paymentNote(*v.Payment))
		if btn, ok := keyboards.Button(c.T("button.bank", nil), AddrBank); ok {
			kb.Row(btn)
		}
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHospital}))
	return c.respond(paragraphs(text, pay), kb.Build()).MarkPrivate()
}

// MethodFree is the method a free treatment is confirmed with.
const MethodFree = "free"

// TreatedView is a treatment given.
type TreatedView struct {
	Option TreatOption
	Paid   int64
	Method string
	// Saved is what it took off the stay; Remaining and EndsAt the stay
	// left.
	Saved     time.Duration
	Remaining time.Duration
	EndsAt    time.Time
}

// Treated renders a treatment given.
func Treated(c Context, v TreatedView) *presenter.Response {
	lines := []string{c.T("health.treated", map[string]any{"provider": c.providerName(v.Option),
		"saved": FormatDuration(c, v.Saved)})}
	if v.Paid > 0 {
		lines = append(lines, c.T("health.paid", map[string]any{"price": FormatMoney(c, v.Paid)}), c.paidLine(v.Method))
	}
	lines = append(lines, c.T("health.remaining", map[string]any{"remaining": FormatDuration(c, v.Remaining)}),
		clockLine(c, "health.discharge_at", v.EndsAt))
	kb := keyboards.New()
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome, RefreshData: AddrHospital}))
	return c.respond(body(lines...), kb.Build()).MarkPrivate()
}

// ClinicDeskView is a clinic's desk, for its owner and manager.
type ClinicDeskView struct {
	Ref   CompanyRef
	Price int64
	Open  bool
	// Stock is its units of medicine; Stocked whether it has enough for a
	// treatment, which takes Units.
	Stock   int64
	Stocked bool
	Units   int
	// Doctor is its best medicine skill; ReductionBPS what a treatment
	// takes off a stay with that doctor.
	Doctor       int
	ReductionBPS int
	Treated      int
	Earned       int64
}

// ClinicDesk renders a clinic's desk.
func ClinicDesk(c Context, v ClinicDeskView) *presenter.Response {
	status := c.T("health.desk.closed", nil)
	if v.Open {
		status = c.T("health.desk.open", nil)
	}
	stock := c.T("health.desk.stock", map[string]any{"units": FormatNumber(c, v.Stock), "per": FormatNumber(c, int64(v.Units))})
	if !v.Stocked {
		stock = body(stock, c.T("health.desk.no_stock", nil))
	}
	lines := []string{
		status,
		c.T("health.desk.price", map[string]any{"price": FormatMoney(c, v.Price)}),
		stock,
		c.T("health.desk.doctor", map[string]any{"level": FormatNumber(c, int64(v.Doctor)),
			"share": PercentFromBPS(c, v.ReductionBPS)}),
		c.T("health.desk.earned", map[string]any{"count": FormatNumber(c, int64(v.Treated)), "earned": FormatMoney(c, v.Earned)}),
	}
	kb := keyboards.New()
	if btn, ok := askButton(c.T("health.button.price", nil), commandClinicPr, v.Ref.Code); ok {
		kb.Row(btn)
	}
	toggle, on := c.T("health.button.open", nil), "on"
	if v.Open {
		toggle, on = c.T("health.button.close", nil), "off"
	}
	if btn, ok := keyboards.Button(toggle, AddrClinicOpen, v.Ref.Code, on); ok {
		kb.Row(btn)
	}
	if btn, ok := keyboards.Button(c.T("production.button.warehouse", nil), AddrWarehouse, v.Ref.Code); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: keyboards.Data(AddrCompanyManage, v.Ref.Code),
		RefreshData: keyboards.Data(AddrClinicDesk, v.Ref.Code)}))
	title := c.T("health.desk.title", map[string]any{"name": v.Ref.Name})
	return c.respond(paragraphs(title, body(lines...), c.T("health.desk.help", nil)), kb.Build()).MarkPrivate()
}

// HospitalisedNoticeView is an admission, as the patient learns of it.
type HospitalisedNoticeView struct {
	CityCode, City string
	Cause          string
	Damage, Health int
	Max            int
	EndsAt         time.Time
	Remaining      time.Duration
}

// HospitalisedNotice tells a player they were taken to hospital.
func HospitalisedNotice(c Context, v HospitalisedNoticeView) *presenter.Response {
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("health.button.hospital", nil), AddrHospital); ok {
		kb.Row(btn)
	}
	text := body(
		c.T("health.notice.hospitalised", map[string]any{"city": c.CityName(v.CityCode, v.City),
			"damage": FormatNumber(c, int64(v.Damage)), "health": FormatNumber(c, int64(v.Health)),
			"max": FormatNumber(c, int64(v.Max))}),
		c.T("health.cause."+v.Cause, nil),
		c.T("health.remaining", map[string]any{"remaining": FormatDuration(c, v.Remaining)}),
		clockLine(c, "health.discharge_at", v.EndsAt),
		c.T("health.notice.blocks", nil),
	)
	return c.respond(text, kb.Build()).MarkPrivate()
}

// DischargedNotice tells a player they left hospital.
func DischargedNotice(c Context, cityCode, city string, health, max int) *presenter.Response {
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("button.map", nil), AddrMap); ok {
		kb.Row(btn)
	}
	return c.respond(c.T("health.notice.discharged", map[string]any{"city": c.CityName(cityCode, city),
		"health": FormatNumber(c, int64(health)), "max": FormatNumber(c, int64(max))}), kb.Build()).MarkPrivate()
}

// ClinicTreatedNoticeView is a treatment a clinic gave, as its owner learns.
type ClinicTreatedNoticeView struct {
	Ref     CompanyRef
	Patient string
	Price   int64
	Item    Named
	Units   int
	Saved   time.Duration
}

// ClinicTreatedNotice tells a clinic's owner it treated a patient.
func ClinicTreatedNotice(c Context, v ClinicTreatedNoticeView) *presenter.Response {
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("health.button.desk", nil), AddrClinicDesk, v.Ref.Code); ok {
		kb.Row(btn)
	}
	return c.respond(c.T("health.notice.clinic_treated", map[string]any{"name": v.Ref.Name, "patient": v.Patient,
		"price": FormatMoney(c, v.Price), "item": c.ItemName(v.Item), "units": FormatNumber(c, int64(v.Units)),
		"saved": FormatDuration(c, v.Saved)}), kb.Build()).MarkPrivate()
}

// HospitalisedLine is the public line a city's groups read: who was taken
// to hospital, never why nor for how long.
func HospitalisedLine(c Context, player, cityCode, city string) string {
	return c.T("health.announce.hospitalised", map[string]any{"player": player, "city": c.CityName(cityCode, city)})
}

// Health refusal kinds.
const (
	HealthRefusedNoHospitals     = "no_hospitals"
	HealthRefusedNotHospitalised = "not_hospitalised"
	HealthRefusedTreated         = "treated"
	HealthRefusedNoClinic        = "no_clinic"
	HealthRefusedElsewhere       = "elsewhere"
	HealthRefusedClosed          = "closed"
	HealthRefusedNoMedicine      = "no_medicine"
	HealthRefusedNotYours        = "not_yours"
)

// HealthRefusalView is a refused health request.
type HealthRefusalView struct{ Kind string }

// HealthRefusal renders a refused health request, with the way back.
func HealthRefusal(c Context, v HealthRefusalView) *presenter.Response {
	kb := keyboards.New()
	if btn, ok := keyboards.Button(c.T("health.button.hospital", nil), AddrHospital); ok {
		kb.Row(btn)
	}
	kb.Nav(c.nav(keyboards.Nav{BackData: AddrHome}))
	return c.respond(c.T("health.refused."+v.Kind, nil), kb.Build()).MarkPrivate()
}

// healthError names the refusal other features raise for a patient.
func healthError(c Context, err error) (string, map[string]any, bool) {
	if !identical(err, application.ErrHospitalised) {
		return "", nil, false
	}
	secs := detailInt(err, "remaining_seconds")
	if secs <= 0 {
		return "health.error.hospitalised_later", nil, true
	}
	return "health.error.hospitalised", map[string]any{"remaining": FormatDuration(c, time.Duration(secs)*time.Second)}, true
}

func init() {
	errorNextStep["health.error.hospitalised"] = struct{ label, addr string }{"health.button.hospital", AddrHospital}
	errorNextStep["health.error.hospitalised_later"] = struct{ label, addr string }{"health.button.hospital", AddrHospital}
}
