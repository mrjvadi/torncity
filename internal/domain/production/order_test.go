package production

import (
	"errors"
	"math/rand/v2"
	"reflect"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/domain/item"
)

// The archetypes, components and method profiles below are content, built as
// literals. Production is given them; it knows none of them.

func breadArchetype() item.Archetype {
	return item.Archetype{
		Code: "bread", Method: item.MethodFormulate,
		Slots: []item.Slot{
			{Name: "flour", Accepts: "flour", Quantity: item.Range(300, 600, "g")},
			{Name: "water", Accepts: "water", Quantity: item.Range(150, 400, "ml")},
			{Name: "salt", Accepts: "salt", Quantity: item.Range(2, 12, "g"), Optional: true},
		},
		Value: item.ValueDerived, Cost: item.CostItemized, Durability: item.DurabilityConsumable,
		ReverseDifficulty: 10, ReverseSkill: "cooking",
	}
}

func phoneArchetype() item.Archetype {
	return item.Archetype{
		Code: "phone", Method: item.MethodAssemble,
		Slots: []item.Slot{
			{Name: "cpu", Accepts: "processor", Quantity: item.Fixed(1)},
			{Name: "speakers", Accepts: "speaker", Quantity: item.Fixed(2)},
		},
		Value: item.ValueDerived, Cost: item.CostItemized, Durability: item.DurabilityDurable,
		ReverseDifficulty: 50, ReverseSkill: "electronics",
	}
}

func components() item.Components {
	return item.Components{
		"flour_whole": {Code: "flour_whole", Category: "flour"},
		"water_tap":   {Code: "water_tap", Category: "water"},
		"salt_sea":    {Code: "salt_sea", Category: "salt"},
		"cpu_a7":      {Code: "cpu_a7", Category: "processor", RequiresTechnology: []string{"semiconductor"}},
		"speaker_s1":  {Code: "speaker_s1", Category: "speaker"},
		"seed_wheat":  {Code: "seed_wheat", Category: "seed"},
		"brick":       {Code: "brick", Category: "masonry"},
	}
}

func breadDesign(flour, water, salt int64) item.Design {
	d := item.Design{Archetype: "bread", Origin: item.OriginAuthored, Fills: map[string]item.Fill{
		"flour": {Component: "flour_whole", Quantity: flour},
		"water": {Component: "water_tap", Quantity: water},
	}}
	if salt > 0 {
		d.Fills["salt"] = item.Fill{Component: "salt_sea", Quantity: salt}
	}
	return d
}

func phoneDesign() item.Design {
	return item.Design{Archetype: "phone", Origin: item.OriginAuthored, Fills: map[string]item.Fill{
		"cpu":      {Component: "cpu_a7", Quantity: 1},
		"speakers": {Component: "speaker_s1", Quantity: 2},
	}}
}

var (
	formulateProfile = Profile{Method: item.MethodFormulate, Setup: 10 * time.Minute, WorkPerUnit: 30 * time.Minute, MachineOutputBPS: 30_000}
	assembleProfile  = Profile{Method: item.MethodAssemble, Setup: time.Hour, WorkPerUnit: 2 * time.Hour, MachineOutputBPS: 20_000}
)

func TestPlanOrderBread(t *testing.T) {
	req := Request{
		Archetype: breadArchetype(), Design: breadDesign(500, 300, 5), Components: components(),
		Quantity: 40, Workers: 2, Machines: 1,
	}
	stock := Stock{"flour_whole": 20_000, "water_tap": 12_000, "salt_sea": 1_000}
	plan, err := PlanOrder(req, formulateProfile, stock)
	if err != nil {
		t.Fatal(err)
	}
	want := item.Recipe{{Component: "flour_whole", Quantity: 20_000}, {Component: "salt_sea", Quantity: 200}, {Component: "water_tap", Quantity: 12_000}}
	if !reflect.DeepEqual(plan.Consumed, want) {
		t.Errorf("consumed %v, want %v", plan.Consumed, want)
	}
	// capacity = 2 workers + 1 machine at 3 workers = 5 worker-equivalents;
	// work = 30 min × 40 / 5 = 240 min; plus 10 min setup.
	if plan.Duration != 250*time.Minute {
		t.Errorf("duration %s, want 4h10m", plan.Duration)
	}
	if plan.Output != 40 || !plan.Goods {
		t.Errorf("output %d goods %v", plan.Output, plan.Goods)
	}
	if stock["flour_whole"] != 20_000 {
		t.Error("PlanOrder changed the stock; consuming it is the caller's write")
	}
}

// TestPlanOrderRefusesShortInputs: never produces from nothing, not even
// part of an order.
func TestPlanOrderRefusesShortInputs(t *testing.T) {
	req := Request{
		Archetype: phoneArchetype(), Design: phoneDesign(), Components: components(),
		Quantity: 10, Workers: 5,
	}
	stock := Stock{"cpu_a7": 9, "speaker_s1": 30}
	_, err := PlanOrder(req, assembleProfile, stock)
	if !errors.Is(err, ErrInsufficientInputs) {
		t.Fatalf("got %v, want ErrInsufficientInputs", err)
	}
	var se *ShortageError
	if !errors.As(err, &se) {
		t.Fatalf("%T is not a *ShortageError", err)
	}
	if want := []Shortage{{Component: "cpu_a7", Need: 10, Have: 9}}; !reflect.DeepEqual(se.Shortages, want) {
		t.Errorf("shortages %v, want %v", se.Shortages, want)
	}

	// Every short input is listed, including one the producer holds none of.
	_, err = PlanOrder(req, assembleProfile, Stock{"cpu_a7": 1})
	if !errors.As(err, &se) || len(se.Shortages) != 2 || se.Shortages[1] != (Shortage{"speaker_s1", 20, 0}) {
		t.Errorf("got %v", err)
	}

	// Exactly enough is enough.
	if _, err := PlanOrder(req, assembleProfile, Stock{"cpu_a7": 10, "speaker_s1": 20}); err != nil {
		t.Errorf("exact stock refused: %v", err)
	}
}

// TestHeldDesignNeedsNoTechnology: a company that holds a copied or bought
// design builds from bought components without the component technology
// (ADR 0005 §5, §6). Production has no technology input at all.
func TestHeldDesignNeedsNoTechnology(t *testing.T) {
	out, err := item.ReverseEngineer(phoneArchetype(), phoneDesign(), item.Engineer{Skill: "electronics", Level: 50}, 0)
	if err != nil || !out.Succeeded {
		t.Fatalf("reverse engineering: %v %+v", err, out)
	}
	req := Request{Archetype: phoneArchetype(), Design: out.Design, Components: components(), Quantity: 4, Workers: 1}
	plan, err := PlanOrder(req, assembleProfile, Stock{"cpu_a7": 100, "speaker_s1": 100})
	if err != nil {
		t.Fatal(err)
	}
	// The copy's 15% overhead: ceil(1 × 1.15) = 2 CPUs and ceil(2 × 1.15)
	// = 3 speakers per phone. The copier pays for not knowing better.
	want := item.Recipe{{Component: "cpu_a7", Quantity: 8}, {Component: "speaker_s1", Quantity: 12}}
	if !reflect.DeepEqual(plan.Consumed, want) {
		t.Errorf("consumed %v, want %v", plan.Consumed, want)
	}
	if plan.QualityLossBPS != 1_500 {
		t.Errorf("quality loss %d not carried to the plan", plan.QualityLossBPS)
	}
}

func TestPlanOrderDuration(t *testing.T) {
	grow := item.Archetype{
		Code: "wheat", Method: item.MethodGrow,
		Slots: []item.Slot{{Name: "seed", Accepts: "seed", Quantity: item.Fixed(1)}},
		Value: item.ValueDerived, Cost: item.CostItemized, Durability: item.DurabilityConsumable, ReverseSkill: "cooking",
	}
	growDesign := item.Design{Archetype: "wheat", Origin: item.OriginAuthored,
		Fills: map[string]item.Fill{"seed": {Component: "seed_wheat", Quantity: 1}}}
	season := 90 * 24 * time.Hour
	growProfile := Profile{Method: item.MethodGrow, Setup: time.Hour, WorkPerUnit: time.Minute, MinCycle: season}
	stock := Stock{"seed_wheat": 1_000_000}

	tests := []struct {
		name    string
		req     Request
		profile Profile
		want    time.Duration
	}{
		{"one worker", Request{Archetype: phoneArchetype(), Design: phoneDesign(), Quantity: 3, Workers: 1},
			assembleProfile, time.Hour + 6*time.Hour},
		{"machines only", Request{Archetype: phoneArchetype(), Design: phoneDesign(), Quantity: 3, Machines: 3},
			assembleProfile, time.Hour + time.Hour}, // 6 h of work at 6 worker-equivalents
		// 2 h / 7 is 1028571428571.43 ns: rounded up, never finished early.
		{"rounds up", Request{Archetype: phoneArchetype(), Design: phoneDesign(), Quantity: 1, Workers: 7},
			assembleProfile, time.Hour + 1_028_571_428_572},
		{"grow waits for the season", Request{Archetype: grow, Design: growDesign, Quantity: 1000, Workers: 1},
			growProfile, time.Hour + season},
		{"more farmers do not ripen wheat", Request{Archetype: grow, Design: growDesign, Quantity: 1000, Workers: 1000},
			growProfile, time.Hour + season},
		{"a huge field outlasts the season", Request{Archetype: grow, Design: growDesign, Quantity: 1_000_000, Workers: 1},
			growProfile, time.Hour + 1_000_000*time.Minute},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.req.Components = components()
			st := stock
			if tc.req.Archetype.Code == "phone" {
				st = Stock{"cpu_a7": 100, "speaker_s1": 200}
			}
			plan, err := PlanOrder(tc.req, tc.profile, st)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Duration != tc.want {
				t.Errorf("duration %s, want %s", plan.Duration, tc.want)
			}
		})
	}
}

func TestPlanOrderMethods(t *testing.T) {
	film := item.Archetype{Code: "film", Method: item.MethodAuthor,
		Value: item.ValueSubjective, Cost: item.CostDiscretionary, Durability: item.DurabilityDurable, ReverseSkill: "engineering"}
	surgery := item.Archetype{Code: "surgery", Method: item.MethodServe,
		Value: item.ValueSubjective, Cost: item.CostItemized, Durability: item.DurabilityConsumable}
	house := item.Archetype{Code: "house", Method: item.MethodConstruct,
		Slots: []item.Slot{{Name: "walls", Accepts: "masonry", Quantity: item.Range(1000, 50_000, "")}},
		Value: item.ValueDerived, Cost: item.CostItemized, Durability: item.DurabilityDurable, ReverseSkill: "engineering"}
	houseDesign := item.Design{Archetype: "house", Origin: item.OriginAuthored,
		Fills: map[string]item.Fill{"walls": {Component: "brick", Quantity: 8_000}}}
	author := Profile{Method: item.MethodAuthor, WorkPerUnit: 100 * time.Hour}
	serve := Profile{Method: item.MethodServe, WorkPerUnit: time.Hour}
	construct := Profile{Method: item.MethodConstruct, WorkPerUnit: 1_000 * time.Hour}

	// Author works from labour alone and makes one work.
	plan, err := PlanOrder(Request{Archetype: film, Design: item.Design{Archetype: "film", Origin: item.OriginAuthored},
		Quantity: 1, Workers: 10}, author, nil)
	if err != nil || len(plan.Consumed) != 0 || plan.Output != 1 || !plan.Goods || plan.Duration != 10*time.Hour {
		t.Errorf("author: %+v, %v", plan, err)
	}
	if _, err := PlanOrder(Request{Archetype: film, Design: item.Design{Archetype: "film", Origin: item.OriginAuthored},
		Quantity: 2, Workers: 10}, author, nil); !errors.Is(err, ErrSingleOutput) {
		t.Errorf("author batch: got %v", err)
	}

	// Serve delivers effects, not goods, and may run many at once.
	plan, err = PlanOrder(Request{Archetype: surgery, Design: item.Design{Archetype: "surgery", Origin: item.OriginAuthored},
		Quantity: 3, Workers: 1}, serve, nil)
	if err != nil || plan.Goods || plan.Output != 3 {
		t.Errorf("serve: %+v, %v", plan, err)
	}

	// Construct consumes materials and makes one property.
	plan, err = PlanOrder(Request{Archetype: house, Design: houseDesign, Components: components(), Quantity: 1, Workers: 20},
		construct, Stock{"brick": 8_000})
	if err != nil || plan.Consumed[0].Quantity != 8_000 || plan.Output != 1 {
		t.Errorf("construct: %+v, %v", plan, err)
	}
	if _, err := PlanOrder(Request{Archetype: house, Design: houseDesign, Components: components(), Quantity: 2, Workers: 20},
		construct, Stock{"brick": 16_000}); !errors.Is(err, ErrSingleOutput) {
		t.Errorf("construct batch: got %v", err)
	}
}

func TestPlanOrderRejects(t *testing.T) {
	onlyOptional := item.Archetype{Code: "kit", Method: item.MethodAssemble,
		Slots: []item.Slot{{Name: "extra", Accepts: "speaker", Quantity: item.Fixed(1), Optional: true}}}
	base := func() Request {
		return Request{Archetype: phoneArchetype(), Design: phoneDesign(), Components: components(), Quantity: 1, Workers: 1}
	}
	plenty := Stock{"cpu_a7": 1_000_000, "speaker_s1": 2_000_000}
	tests := []struct {
		name   string
		mutate func(*Request, *Profile)
		want   error
	}{
		{"profile for another method", func(_ *Request, p *Profile) { *p = formulateProfile }, ErrProfileMismatch},
		{"negative setup", func(_ *Request, p *Profile) { p.Setup = -1 }, ErrInvalidProfile},
		{"machine too strong", func(_ *Request, p *Profile) { p.MachineOutputBPS = MaxMachineOutputBPS + 1 }, ErrInvalidProfile},
		{"design does not fit", func(r *Request, _ *Profile) { delete(r.Design.Fills, "cpu") }, item.ErrRequiredSlotEmpty},
		{"zero quantity", func(r *Request, _ *Profile) { r.Quantity = 0 }, ErrInvalidQuantity},
		{"quantity above max", func(r *Request, _ *Profile) { r.Quantity = MaxOrderQuantity + 1 }, ErrInvalidQuantity},
		{"nobody working", func(r *Request, _ *Profile) { r.Workers = 0 }, ErrNoCapacity},
		{"machines with no output", func(r *Request, p *Profile) { r.Workers = 0; r.Machines = 5; p.MachineOutputBPS = 0 }, ErrNoCapacity},
		{"negative workers", func(r *Request, _ *Profile) { r.Workers = -1 }, ErrInvalidCrew},
		{"too many machines", func(r *Request, _ *Profile) { r.Machines = MaxCrew + 1 }, ErrInvalidCrew},
		{"empty recipe for a material method", func(r *Request, _ *Profile) {
			r.Archetype = onlyOptional
			r.Design = item.Design{Archetype: "kit", Origin: item.OriginAuthored}
		}, ErrNothingFromNothing},
		{"too long", func(r *Request, p *Profile) { r.Quantity = MaxOrderQuantity; p.WorkPerUnit = MaxWorkPerUnit }, ErrTooLong},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, prof := base(), assembleProfile
			tc.mutate(&req, &prof)
			if _, err := PlanOrder(req, prof, plenty); !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
	if err := ValidateProfile(Profile{Method: "weave"}); !errors.Is(err, item.ErrUnknownMethod) {
		t.Errorf("unknown method profile: got %v", err)
	}
}

// TestConsumedEqualsRecipeTimesQuantity is the property the whole package
// rests on: whatever the design, overhead and quantity, an order consumes
// exactly the derived recipe times the quantity — never less — and is
// accepted exactly when the stock covers that.
func TestConsumedEqualsRecipeTimesQuantity(t *testing.T) {
	rng := rand.New(rand.NewPCG(24, 5))
	a := breadArchetype()
	for i := 0; i < 3000; i++ {
		var salt int64
		if rng.IntN(2) == 0 {
			salt = 2 + rng.Int64N(11)
		}
		d := breadDesign(300+rng.Int64N(301), 150+rng.Int64N(251), salt)
		d.OverheadBPS = rng.Int64N(item.MaxOverheadBPS + 1)
		qty := 1 + rng.Int64N(500)

		perUnit, err := item.DeriveRecipe(d)
		if err != nil {
			t.Fatal(err)
		}
		want, err := perUnit.Times(qty)
		if err != nil {
			t.Fatal(err)
		}

		// Stock around the requirement: sometimes enough, sometimes short.
		stock := Stock{}
		covered := true
		for _, in := range want {
			have := in.Quantity + rng.Int64N(21) - 10
			stock[in.Component] = have
			if have < in.Quantity {
				covered = false
			}
		}

		req := Request{Archetype: a, Design: d, Components: components(), Quantity: qty, Workers: 1 + rng.IntN(10)}
		plan, err := PlanOrder(req, formulateProfile, stock)
		if covered != (err == nil) {
			t.Fatalf("stock %v for %v: covered %v, err %v", stock, want, covered, err)
		}
		if !covered {
			if !errors.Is(err, ErrInsufficientInputs) {
				t.Fatalf("short stock refused with %v", err)
			}
			continue
		}
		if !reflect.DeepEqual(plan.Consumed, want) {
			t.Fatalf("consumed %v, want %v", plan.Consumed, want)
		}
		for _, in := range plan.Consumed {
			fill := int64(0)
			for _, f := range d.Fills {
				if f.Component == in.Component {
					fill += f.Quantity
				}
			}
			if in.Quantity < fill*qty {
				t.Fatalf("%s: consumed %d, below %d × %d", in.Component, in.Quantity, fill, qty)
			}
			if stock[in.Component]-in.Quantity < 0 {
				t.Fatalf("%s: stock would go negative", in.Component)
			}
		}
		if plan.Output != qty {
			t.Fatalf("output %d for quantity %d", plan.Output, qty)
		}
	}
}

// TestPlanOrderRefusesRetiredDesign: a design the company retired is no
// longer producible — new production stops, but nothing about existing
// stock or PlanOrder's other checks changes.
func TestPlanOrderRefusesRetiredDesign(t *testing.T) {
	d := breadDesign(500, 300, 5)
	d.ID = "bread-v1"
	d.Retired = true
	req := Request{Archetype: breadArchetype(), Design: d, Components: components(), Quantity: 10, Workers: 2}
	stock := Stock{"flour_whole": 20_000, "water_tap": 12_000, "salt_sea": 1_000}
	_, err := PlanOrder(req, formulateProfile, stock)
	if !errors.Is(err, ErrDesignRetired) {
		t.Fatalf("err = %v, want ErrDesignRetired", err)
	}
}
