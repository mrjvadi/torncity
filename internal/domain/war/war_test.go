package war

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

func TestWarStatusAndSides(t *testing.T) {
	w := War{ID: "w", Attacker: "a", Defender: "d", Status: Declared, ActiveAt: t0.Add(time.Hour),
		Parties: []Party{{Country: "a", Side: Attacker}, {Country: "d", Side: Defender}, {Country: "ally", Side: Defender}}}
	if w.StatusAt(t0) != Declared || w.StatusAt(t0.Add(time.Hour)) != Active {
		t.Fatalf("status before and after the notice: %s, %s", w.StatusAt(t0), w.StatusAt(t0.Add(time.Hour)))
	}
	if w.Hostile("a", "d", t0) {
		t.Error("hostile before the notice ran")
	}
	if !w.Hostile("a", "ally", t0.Add(2*time.Hour)) || w.Hostile("d", "ally", t0.Add(2*time.Hour)) {
		t.Error("an ally is the attacker's enemy and the defender's friend")
	}
	if s, ok := w.SideOf("ally"); !ok || s != Defender {
		t.Errorf("the ally's side = %s, %v", s, ok)
	}
	if !AtWar([]War{w}, "ally", t0.Add(2*time.Hour)) || AtWar([]War{w}, "neutral", t0.Add(2*time.Hour)) {
		t.Error("AtWar")
	}
	if err := CheckDeclare("a", "d", []War{w}); !errors.Is(err, ErrAtWar) {
		t.Errorf("a second declaration = %v", err)
	}
	if err := CheckDeclare("ally", "a", []War{w}); !errors.Is(err, ErrAtWar) {
		t.Errorf("an ally declaring on the enemy it fights = %v", err)
	}
	if err := CheckDeclare("a", "a", nil); !errors.Is(err, ErrSelf) {
		t.Errorf("war on oneself = %v", err)
	}
	if err := CheckJoin(w, "ally", Defender, t0); !errors.Is(err, ErrAtWar) {
		t.Errorf("joining twice = %v", err)
	}
	if err := CheckJoin(w, "d", Attacker, t0); !errors.Is(err, ErrAtWar) {
		t.Errorf("the defender joining the attack = %v", err)
	}
	if err := CheckJoin(w, "other", Attacker, t0); err != nil {
		t.Errorf("a neutral joining = %v", err)
	}
	ended := w
	ended.Status = Ended
	if _, ok := Between([]War{ended}, "a", "d"); ok {
		t.Error("an ended war is still between them")
	}
}

func TestCeasefireAndPeace(t *testing.T) {
	now := t0.Add(2 * time.Hour)
	w := War{Attacker: "a", Defender: "d", Status: Declared, ActiveAt: t0}
	cf := Proposal{Kind: ProposeCeasefire, Proposer: "a", Partner: "d", Status: ProposalOpen, ExpiresAt: now.Add(time.Hour)}
	if err := CheckPropose(w, "ally", ProposeCeasefire, nil, now); !errors.Is(err, ErrNotParty) {
		t.Errorf("an ally proposing = %v", err)
	}
	if err := CheckPropose(w, "a", ProposeCeasefire, []Proposal{cf}, now); !errors.Is(err, ErrProposalOpen) {
		t.Errorf("a second open ceasefire = %v", err)
	}
	if _, _, err := Answer(w, cf, "a", true, now); !errors.Is(err, ErrNotParty) {
		t.Errorf("the proposer answering = %v", err)
	}
	ps, ws, err := Answer(w, cf, "d", true, now)
	if err != nil || ps != ProposalAccepted || ws != Ceasefire {
		t.Fatalf("accepting a ceasefire = %s, %s, %v", ps, ws, err)
	}
	w.Status = ws
	if w.Hostile("a", "d", now) {
		t.Error("hostile under a ceasefire")
	}
	if err := CheckPropose(w, "d", ProposeCeasefire, nil, now); !errors.Is(err, ErrState) {
		t.Errorf("a ceasefire in a ceasefire = %v", err)
	}
	if err := CheckResume(w, "d"); err != nil {
		t.Errorf("resuming = %v", err)
	}
	peace := Proposal{Kind: ProposePeace, Proposer: "d", Partner: "a", Status: ProposalOpen, ExpiresAt: now.Add(time.Hour)}
	if _, _, err := Answer(w, peace, "a", true, now.Add(time.Hour)); !errors.Is(err, ErrState) {
		t.Errorf("accepting an expired proposal = %v", err)
	}
	ps, ws, err = Answer(w, peace, "a", true, now)
	if err != nil || ps != ProposalAccepted || ws != Ended {
		t.Fatalf("accepting peace = %s, %s, %v", ps, ws, err)
	}
	w.Status = ws
	if _, _, err := Answer(w, cf, "d", true, now); !errors.Is(err, ErrState) {
		t.Errorf("a ceasefire after the peace = %v", err)
	}
	if s, err := Withdraw(peace, "a", now); !errors.Is(err, ErrNotParty) || s != ProposalOpen {
		t.Errorf("the partner withdrawing = %s, %v", s, err)
	}
}

func doctrine() Doctrine {
	return Doctrine{ReactionSeconds: 45, ShotsPerTarget: 2, PkFloorBPS: 500, PkCeilingBPS: 9500, HorizonKM: 40,
		CueBPS: 5000, AirToAirKM: 100, AirToAirPkBPS: 6000}
}

// defence is a city defended by a long-wave early-warning radar, a
// long-range battery and a short-range one.
func defence(longCount, rounds int64) ([]Sensor, []Layer) {
	return []Sensor{{RangeKM: 350, GainBPS: 200_000}},
		[]Layer{
			{Code: "long", Count: longCount, RadarKM: 200, GainBPS: 10_000, ReachKM: 250, PkBPS: 8000, Rounds: rounds,
				Quality: 60, Ballistic: true},
			{Code: "short", Count: 2, RadarKM: 110, GainBPS: 10_000, ReachKM: 12, PkBPS: 7000, Rounds: rounds, Quality: 55},
		}
}

func fighters(n int, rcs int64) []Threat {
	out := make([]Threat, n)
	for i := range out {
		out[i] = Threat{Kind: Aircraft, RCSMilli: rcs, SpeedKMH: 2100, Quality: 60, Load: 2}
	}
	return out
}

func TestStealthShortensTheWindow(t *testing.T) {
	d := doctrine()
	sensors, layers := defence(2, 4)
	stealth := Threat{Kind: Aircraft, RCSMilli: 5, SpeedKMH: 2100}
	plain := Threat{Kind: Aircraft, RCSMilli: 5000, SpeedKMH: 2100}
	cue := func(th Threat) int64 {
		return d.seen(sensors[0].RangeKM, sensors[0].GainBPS, th.RCSMilli, false) * d.CueBPS / BPSWhole
	}
	cs := d.Cycles(d.engagementKM(layers[0], cue(stealth), stealth), stealth.SpeedKMH)
	cp := d.Cycles(d.engagementKM(layers[0], cue(plain), plain), plain.SpeedKMH)
	if cs >= cp || cs < 1 {
		t.Fatalf("cycles against a stealth fighter %d, a conventional one %d: stealth must buy time, not vanish", cs, cp)
	}
	out, err := Resolve(Strike{Seed: 7, Threats: []Threat{stealth, plain}, Munitions: 4, Munition: Munition{Accuracy: 8000,
		Firepower: 500, Quality: 55}, AttackerReadiness: BPSWhole, DefenderReadiness: BPSWhole, Sensors: sensors,
		Layers: layers, Doctrine: d})
	if err != nil {
		t.Fatal(err)
	}
	if out.Threats[0].SeenAtKM >= out.Threats[1].SeenAtKM {
		t.Errorf("the stealth fighter was seen at %d km, the conventional one at %d", out.Threats[0].SeenAtKM,
			out.Threats[1].SeenAtKM)
	}
	again, _ := Resolve(Strike{Seed: 7, Threats: []Threat{stealth, plain}, Munitions: 4, Munition: Munition{Accuracy: 8000,
		Firepower: 500, Quality: 55}, AttackerReadiness: BPSWhole, DefenderReadiness: BPSWhole, Sensors: sensors,
		Layers: layers, Doctrine: d})
	if !reflect.DeepEqual(out, again) {
		t.Error("the same strike resolved twice differently")
	}
}

func TestLowFlyingMissilesHideUnderTheHorizon(t *testing.T) {
	d := doctrine()
	sensors, layers := defence(1, 4)
	cruise := Threat{Kind: Missile, RCSMilli: 100, SpeedKMH: 900, LowLevel: true, Accuracy: 9000, Firepower: 500}
	out, err := Resolve(Strike{Seed: 1, Threats: []Threat{cruise}, Sensors: sensors, Layers: layers, Doctrine: d,
		AttackerReadiness: BPSWhole, DefenderReadiness: BPSWhole})
	if err != nil {
		t.Fatal(err)
	}
	if out.Threats[0].SeenAtKM != d.HorizonKM {
		t.Errorf("a cruise missile seen at %d km, want the horizon %d", out.Threats[0].SeenAtKM, d.HorizonKM)
	}
}

func TestOnlyBallisticLayersEngageBallisticMissiles(t *testing.T) {
	d := doctrine()
	_, layers := defence(0, 4) // no long-range battery: only the short one
	salvo := []Threat{{Kind: Missile, RCSMilli: 100, SpeedKMH: 7000, Ballistic: true, Accuracy: 6000, Firepower: 500}}
	out, err := Resolve(Strike{Seed: 3, Threats: salvo, Layers: layers, Doctrine: d, AttackerReadiness: BPSWhole,
		DefenderReadiness: BPSWhole})
	if err != nil {
		t.Fatal(err)
	}
	if out.Threats[0].Shots != 0 || out.Threats[0].Lost {
		t.Errorf("a short-range battery engaged a ballistic missile: %+v", out.Threats[0])
	}
}

func TestASalvoLargerThanTheMagazineLeaks(t *testing.T) {
	d := doctrine()
	sensors, layers := defence(1, 2) // one long-range launcher, two rounds
	layers = layers[:1]
	salvo := make([]Threat, 10)
	for i := range salvo {
		salvo[i] = Threat{Kind: Missile, RCSMilli: 100, SpeedKMH: 7000, Ballistic: true, Accuracy: 9000, Firepower: 500}
	}
	out, err := Resolve(Strike{Seed: 5, Threats: salvo, Sensors: sensors, Layers: layers, Doctrine: d,
		AttackerReadiness: BPSWhole, DefenderReadiness: BPSWhole})
	if err != nil {
		t.Fatal(err)
	}
	if out.Layers[0].Fired != 2 || out.Lost > 1 {
		t.Fatalf("fired %d, lost %d: two rounds can stop at most one missile", out.Layers[0].Fired, out.Lost)
	}
	arrived := int64(0)
	for _, f := range out.Threats {
		arrived += f.Delivered
	}
	if arrived < 9 {
		t.Errorf("%d missiles arrived; the salvo must saturate the battery", arrived)
	}
}

// The properties: over many random strikes, a stealthier threat is never
// seen farther off, and more interceptors — more launchers or more rounds —
// never shoot fewer threats down and never let more firepower through.
func TestPropertyMoreStealthNeverEasierToSee(t *testing.T) {
	d := doctrine()
	r := rand.New(rand.NewSource(1))
	for range 2000 {
		sensors, layers := defence(int64(r.Intn(4)), int64(1+r.Intn(4)))
		a, b := int64(1+r.Intn(100_000)), int64(1+r.Intn(100_000))
		if a > b {
			a, b = b, a
		}
		speed := int64(300 + r.Intn(8000))
		low, high := Threat{Kind: Aircraft, RCSMilli: a, SpeedKMH: speed}, Threat{Kind: Aircraft, RCSMilli: b, SpeedKMH: speed}
		out, err := Resolve(Strike{Seed: r.Int63(), Threats: []Threat{low, high}, Sensors: sensors, Layers: layers, Doctrine: d})
		if err != nil {
			t.Fatal(err)
		}
		if out.Threats[0].SeenAtKM > out.Threats[1].SeenAtKM {
			t.Fatalf("rcs %d seen at %d km, rcs %d at %d", a, out.Threats[0].SeenAtKM, b, out.Threats[1].SeenAtKM)
		}
		for _, l := range layers {
			cueA := d.seen(sensors[0].RangeKM, sensors[0].GainBPS, a, false) * d.CueBPS / BPSWhole
			cueB := d.seen(sensors[0].RangeKM, sensors[0].GainBPS, b, false) * d.CueBPS / BPSWhole
			if d.Cycles(d.engagementKM(l, cueA, low), speed) > d.Cycles(d.engagementKM(l, cueB, high), speed) {
				t.Fatalf("layer %s gets more cycles at rcs %d than at %d", l.Code, a, b)
			}
		}
	}
}

func TestPropertyMoreInterceptorsNeverRaiseDamage(t *testing.T) {
	d := doctrine()
	r := rand.New(rand.NewSource(2))
	for range 1500 {
		var threats []Threat
		for range 1 + r.Intn(12) {
			if r.Intn(2) == 0 {
				threats = append(threats, Threat{Kind: Aircraft, RCSMilli: int64(1 + r.Intn(20_000)),
					SpeedKMH: int64(500 + r.Intn(2000)), Quality: r.Intn(101), Load: int64(r.Intn(4)),
					AirToAir: int64(r.Intn(3)), RadarKM: 150, GainBPS: 10_000})
			} else {
				threats = append(threats, Threat{Kind: Missile, RCSMilli: int64(1 + r.Intn(500)),
					SpeedKMH: int64(700 + r.Intn(9000)), Ballistic: r.Intn(2) == 0, LowLevel: r.Intn(2) == 0,
					EvasionBPS: int64(r.Intn(4000)), Accuracy: int64(r.Intn(10_001)), Firepower: int64(r.Intn(900))})
			}
		}
		base := Strike{Seed: r.Int63(), Threats: threats, Munitions: int64(r.Intn(20)),
			Munition: Munition{Accuracy: 8000, Firepower: 500, Quality: 55}, AttackerReadiness: int64(r.Intn(10_001)),
			DefenderReadiness: int64(r.Intn(10_001)), Sensors: []Sensor{{RangeKM: 350, GainBPS: 200_000}}, Doctrine: d}
		base.Layers = []Layer{
			{Code: "cap", Count: int64(r.Intn(3)), RadarKM: 110, GainBPS: 10_000, ReachKM: 100, PkBPS: 6000, Rounds: 2,
				Quality: 50, Fighter: true, RCSMilli: 5000, SpeedKMH: 2100},
			{Code: "long", Count: int64(r.Intn(3)), RadarKM: 200, GainBPS: 10_000, ReachKM: 250, PkBPS: 8000,
				Rounds: int64(r.Intn(5)), Quality: 60, Ballistic: true},
			{Code: "short", Count: int64(r.Intn(3)), RadarKM: 110, GainBPS: 10_000, ReachKM: 12, PkBPS: 7000,
				Rounds: int64(r.Intn(5)), Quality: 55},
		}
		more := base
		more.Layers = append([]Layer(nil), base.Layers...)
		li := 1 + r.Intn(2)
		if r.Intn(2) == 0 {
			more.Layers[li].Count += int64(1 + r.Intn(3))
		} else {
			more.Layers[li].Rounds += int64(1 + r.Intn(3))
		}
		a, err := Resolve(base)
		if err != nil {
			t.Fatal(err)
		}
		b, err := Resolve(more)
		if err != nil {
			t.Fatal(err)
		}
		if b.Points > a.Points {
			t.Fatalf("more interceptors let %d points through, fewer %d", b.Points, a.Points)
		}
		for i := range a.Threats {
			if a.Threats[i].Lost && !b.Threats[i].Lost {
				t.Fatalf("threat %d was shot down by fewer interceptors and not by more", i)
			}
		}
	}
}

func TestSuppress(t *testing.T) {
	hits := Suppress(9, 5, 3, 10_000)
	if len(hits) != 3 || !hits[0].Destroyed || hits[2].Asset != 2 {
		t.Fatalf("hits = %+v", hits)
	}
	if len(Suppress(9, 0, 3, 5000)) != 0 {
		t.Error("no hit suppressed something")
	}
	for _, h := range Suppress(9, 3, 3, 0) {
		if h.Destroyed {
			t.Error("a hit destroyed at a zero chance")
		}
	}
}

func ground(n int, firepower, armour int64) []Unit {
	out := make([]Unit, n)
	for i := range out {
		out[i] = Unit{Firepower: firepower, Armour: armour, Accuracy: 7000, Quality: 55}
	}
	return out
}

func assault(att, def []Unit, seed int64) Assault {
	return Assault{Seed: seed, Attackers: att, Defenders: def, AttackerReadiness: BPSWhole, DefenderReadiness: BPSWhole,
		Rounds: 3, DefenderAdvantageBPS: 15_000, BaseHitBPS: 5000, HitFloorBPS: 500, HitCeilingBPS: 9500, HoldMin: 2}
}

func TestOverwhelmingForceTakesTheCity(t *testing.T) {
	out, err := Fight(assault(ground(12, 800, 700), ground(2, 200, 150), 11))
	if err != nil {
		t.Fatal(err)
	}
	if !out.Taken || out.Holding < 2 {
		t.Fatalf("twelve tanks against two militia: %+v", out)
	}
	again, _ := Fight(assault(ground(12, 800, 700), ground(2, 200, 150), 11))
	if !reflect.DeepEqual(out, again) {
		t.Error("the same assault fought twice differently")
	}
	lost, err := Fight(assault(ground(1, 250, 200), ground(12, 800, 700), 11))
	if err != nil || lost.Taken {
		t.Fatalf("one vehicle against twelve tanks took the city: %+v, %v", lost, err)
	}
	art := ground(3, 600, 200)
	for i := range art {
		art[i].Artillery = true
	}
	if out, _ := Fight(assault(art, nil, 1)); out.Taken {
		t.Error("artillery alone held a city")
	}
}

func TestPropertyStrongerAssaultsWinMoreOften(t *testing.T) {
	wins := func(n int) int {
		w := 0
		for seed := range int64(200) {
			if out, _ := Fight(assault(ground(n, 800, 700), ground(4, 500, 500), seed)); out.Taken {
				w++
			}
		}
		return w
	}
	if weak, strong := wins(3), wins(12); strong <= weak {
		t.Errorf("twelve tanks took the city %d times in 200, three %d", strong, weak)
	}
}

func TestCityDamageAndRecovery(t *testing.T) {
	r := CityRules{Structure: 20_000, MaxBPS: 9000, RecoveryPerHour: 50, WealthLossBPS: 8000}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	d := r.Strike(0, 4000)
	if d != 2000 {
		t.Fatalf("4000 points on a 20000 city = %d bps, want 2000", d)
	}
	if got := r.Strike(8500, 100_000); got != 9000 {
		t.Errorf("damage capped at %d, want 9000", got)
	}
	// At a scale of 60, ten real minutes are ten game hours: 500 bps healed.
	if got := r.DamageAt(2000, t0, t0.Add(10*time.Minute), 60); got != 1500 {
		t.Errorf("after ten game hours = %d, want 1500", got)
	}
	if got := r.DamageAt(2000, t0, t0.Add(24*time.Hour), 60); got != 0 {
		t.Errorf("after a long while = %d, want 0", got)
	}
	if got := r.Spending(5000); got != 6000 {
		t.Errorf("spending at half damage = %d, want 6000", got)
	}
	bands := []Band{{Code: "light", UpTo: 1500}, {Code: "heavy", UpTo: 6000}, {Code: "ruin"}}
	if BandOf(bands, 0) != "" || BandOf(bands, 1500) != "light" || BandOf(bands, 9000) != "ruin" {
		t.Error("BandOf")
	}
	if !InReach(700, 1400, true) || InReach(701, 1400, true) || !InReach(1400, 1400, false) {
		t.Error("InReach")
	}
}

func TestRollIsStable(t *testing.T) {
	if Roll(1, 2, 3) != Roll(1, 2, 3) || Roll(1, 2, 3) == Roll(1, 2, 4) && Roll(1, 2, 5) == Roll(1, 2, 6) {
		t.Error("Roll is not a stable function of its coordinates")
	}
	for i := range int64(1000) {
		if v := Roll(i, i); v < 0 || v >= BPSWhole {
			t.Fatalf("Roll out of range: %d", v)
		}
	}
}
