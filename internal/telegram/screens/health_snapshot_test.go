package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
	"github.com/mrjvadi/torncity/internal/telegram/screens/screentest"
)

// Health and hospitals join the snapshot harness as an area of their own —
// testdata/snapshots/<language>/health.txt — and their group line joins
// group.txt.
func init() { snapshotAreas["health"] = healthSnapshots }

// clinicRef is a sample clinic.
func clinicRef(c Context) CompanyRef {
	names := map[string]string{"fa": "شفا", "en": "Shefa"}
	return CompanyRef{Code: "C1N1C2A", Name: names[c.Lang], Type: Named{Code: "clinic", Name: "Clinic"}}
}

func healthSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	clinic := clinicRef(c)
	city := TreatOption{Provider: application.ProviderCity, Price: 750, Saves: 2*time.Minute + 6*time.Second, Open: true, CanTreat: true}
	good := TreatOption{Provider: application.ProviderClinic, Clinic: clinic, Price: 1200, Saves: 4*time.Minute + 30*time.Second,
		Doctor: 12, Stock: 18, Open: true, CanTreat: true}
	empty := TreatOption{Provider: application.ProviderClinic, Clinic: CompanyRef{Code: "D4R0B8K", Name: companyNames[c.Lang][0]},
		Price: 900, Doctor: 3, Open: true}
	closed := TreatOption{Provider: application.ProviderClinic, Clinic: CompanyRef{Code: "M3D1C4L", Name: companyNames[c.Lang][1]},
		Price: 500, Doctor: 1}

	add("Hospital · well, resting back to full", Hospital(c, HospitalView{Health: 72, Max: 100, FullIn: 7 * time.Minute,
		CityCode: "ostmarch", City: "Ostmarch", Clinics: []TreatOption{good, closed}}))
	add("Hospital · in hospital, three ways to be treated", Hospital(c, HospitalView{Health: 23, Max: 100,
		CityCode: "ostmarch", City: "Ostmarch", InHospital: true, Cause: application.CauseCrime,
		Remaining: 6 * time.Minute, EndsAt: snapshotNow.Add(6 * time.Minute), CityHospital: &city,
		Clinics: []TreatOption{good, empty, closed}}))
	add("Hospital · treated, resting", Hospital(c, HospitalView{Health: 41, Max: 100,
		CityCode: "ostmarch", City: "Ostmarch", InHospital: true, Cause: application.CauseWar,
		Remaining: 90 * time.Second, EndsAt: snapshotNow.Add(90 * time.Second), Treated: true, TreatedBy: good}))
	add("Hospital · full health, nowhere", Hospital(c, HospitalView{Health: 100, Max: 100}))

	add("Treat · confirm at a clinic", TreatConfirm(c, TreatConfirmView{Option: good, Remaining: 6 * time.Minute,
		EndsAt: snapshotNow.Add(90 * time.Second),
		Payment: &PaymentChoice{Amount: 1200, Accepted: []string{MethodCash, MethodCard}, Usable: []string{MethodCash, MethodCard},
			Cash: 3400, Bank: 52000}}))
	add("Treat · confirm, cannot afford", TreatConfirm(c, TreatConfirmView{Option: city, Remaining: 6 * time.Minute,
		EndsAt:  snapshotNow.Add(4 * time.Minute),
		Payment: &PaymentChoice{Amount: 750, Accepted: []string{MethodCash, MethodCard}, Cash: 100, Bank: 0}}))
	free := good
	free.Price = 0
	add("Treat · confirm, a free clinic", TreatConfirm(c, TreatConfirmView{Option: free, Remaining: 6 * time.Minute,
		EndsAt: snapshotNow.Add(90 * time.Second)}))
	add("Treat · treated at a clinic", Treated(c, TreatedView{Option: good, Paid: 1200, Method: MethodCard,
		Saved: 4*time.Minute + 30*time.Second, Remaining: 90 * time.Second, EndsAt: snapshotNow.Add(90 * time.Second)}))

	add("Clinic desk · open, stocked", ClinicDesk(c, ClinicDeskView{Ref: clinic, Price: 1200, Open: true, Stock: 18,
		Stocked: true, Units: 1, Doctor: 12, ReductionBPS: 7300, Treated: 9, Earned: 10800}))
	add("Clinic desk · closed, no medicine", ClinicDesk(c, ClinicDeskView{Ref: clinic, Stock: 0, Units: 1,
		ReductionBPS: 5500}))

	add("Notice · taken to hospital", HospitalisedNotice(sent(c), HospitalisedNoticeView{CityCode: "ostmarch", City: "Ostmarch",
		Cause: application.CauseWork, Damage: 31, Health: 14, Max: 100, Remaining: 6 * time.Minute,
		EndsAt: snapshotNow.Add(6 * time.Minute)}))
	add("Notice · discharged", DischargedNotice(sent(c), "ostmarch", "Ostmarch", 60, 100))
	add("Notice · the clinic treated a patient", ClinicTreatedNotice(sent(c), ClinicTreatedNoticeView{Ref: clinic,
		Patient: who.friend, Price: 1200, Item: Named{Code: "bandage", Name: "Bandage"}, Units: 1, Saved: 4 * time.Minute}))

	for _, kind := range []string{HealthRefusedNoHospitals, HealthRefusedNotHospitalised, HealthRefusedTreated,
		HealthRefusedNoClinic, HealthRefusedElsewhere, HealthRefusedClosed, HealthRefusedNoMedicine, HealthRefusedNotYours} {
		add("Refused · "+kind, HealthRefusal(c, HealthRefusalView{Kind: kind}))
	}
	add("Error · asked to travel from a hospital bed",
		Error(c, application.ErrHospitalised.WithDetail("remaining_seconds", int64(300))))

	hurt := &InjuryView{Damage: 12, Health: 58, Max: 100}
	admitted := &InjuryView{Damage: 45, Health: 12, Max: 100, Hospital: true, EndsAt: snapshotNow.Add(6 * time.Minute)}
	burgle := Named{Code: "home_burglary", Name: "Home burglary"}
	resid := Named{Code: "residential_area", Name: "Residential area"}
	heat := HeatView{Heat: 31, Max: 100, Wanted: 2, Stars: 5}
	add("Crime · caught and hurt, off to hospital", CrimeResult(sent(c), CrimeResultView{Player: who.me, Crime: burgle,
		Venue: resid, CityCode: "ostmarch", City: "Ostmarch", Result: CrimeOutcomeCaught, Heat: heat, Notice: true,
		Jail: &CrimeProgress{Remaining: 9 * time.Minute, EndsAt: snapshotNow.Add(9 * time.Minute)}, Injury: admitted}))
	add("Crime · escaped with a scratch", CrimeResult(c, CrimeResultView{Player: who.me, Crime: burgle, Venue: resid,
		CityCode: "ostmarch", City: "Ostmarch", Result: CrimeOutcomeEscaped, Heat: heat,
		Nerve: NerveView{Nerve: 6, Max: 20, FullIn: 40 * time.Minute}, Injury: hurt}))
	add("Crime · as a group reads it", CrimeResult(group(c), CrimeResultView{Player: who.me, Crime: burgle, Venue: resid,
		CityCode: "ostmarch", City: "Ostmarch", Result: CrimeOutcomeEscaped, Heat: heat, Injury: admitted}))
	add("Crime · refused, in hospital", CrimeRefusal(c, CrimeRefusalView{Kind: CrimeRefusedHospital, Remaining: 5 * time.Minute}))
	add("Shift · an accident at work", ShiftWorked(sent(c), ShiftWorkedView{Gross: 180, Net: 162, Tax: 18, XP: 12,
		Performance: 64, PerformanceDelta: 2, Energy: 60, MaxEnergy: 100, Injury: admitted}))
	add("War · a strike on the city hurt you", WarNotice(sent(c), WarNoticeView{Kind: "struck", Country: homeCountry,
		Other: otherCountry, CityCode: "kessmoor", City: "Kessmoor", Band: "moderate", Injury: admitted}))
	add("Profile · in hospital", Profile(c, ProfileView{Name: who.me, Code: myCode, CityCode: "ostmarch", City: "Ostmarch",
		Level: 4, XP: 520, NextLevelXP: 750, Energy: 70, MaxEnergy: 100, Health: 34, MaxHealth: 100, Cash: 2400, Bank: 18000,
		Hospital: &ProfileJail{CityCode: "ostmarch", City: "Ostmarch", Remaining: 4 * time.Minute, EndsAt: snapshotNow.Add(4 * time.Minute)},
		Work:     &ProfileWork{}}))
}

// healthAnnouncements are the public lines a city's groups read of health;
// they join the group lines (group.txt).
func healthAnnouncements(c Context, who people, book *screentest.Book) {
	book.AddText("announcement · taken to hospital", HospitalisedLine(c, who.friend, "ostmarch", "Ostmarch"))
	book.AddText("question · health.price", InputPrompt(c, "health.price", ""))
	book.AddText("question · health.price · reply box", InputPlaceholder(c, "health.price"))
}
