package content

import (
	"errors"
	"strings"
	"testing"
)

// TestShippedAvailabilityIsComplete is the content lint for the availability
// tags (configs/content/availability.yml): every player-facing entry has a
// stage and prerequisites, every reference resolves, and every prerequisite
// is reachable somewhere (Support for the foundational ones). It runs next to
// the village reachability lint it builds on.
func TestShippedAvailabilityIsComplete(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatalf("the shipped content does not parse: %v", err)
	}
	if len(pack.Availability) == 0 {
		t.Fatal("the shipped content has no availability tags: configs/content/availability.yml is missing")
	}
	var problems []error
	pack.validateAvailability(&problems)
	if len(problems) != 0 {
		t.Fatalf("availability lint: %v", errors.Join(problems...))
	}
	universe := pack.availabilityUniverse()
	for _, a := range pack.Availability {
		found := false
		for _, c := range universe[a.Kind] {
			found = found || c == a.Code
		}
		if !found {
			t.Errorf("stale tag %s/%s: no such entry in the content", a.Kind, a.Code)
		}
	}
	undecided := 0
	for _, a := range pack.Availability {
		if a.Stage == StageUndecided {
			undecided++
		}
	}
	t.Logf("%d tags, %d undecided (owner questions)", len(pack.Availability), undecided)
}

func availPack() *Pack {
	p := reachPack()
	p.Skills = []SkillDef{{Code: "cooking", Name: "Cooking"}}
	p.Courses = []CourseDef{{Code: "bread_school", Name: "Bread", Certifies: true}}
	return p
}

func availProblems(p *Pack) string {
	var problems []error
	p.validateAvailability(&problems)
	if len(problems) == 0 {
		return ""
	}
	return errors.Join(problems...).Error()
}

func tagBase(p *Pack, extra ...AvailabilityDef) {
	p.Availability = []AvailabilityDef{
		{Kind: "knowledge", Code: "oral_tradition", Stage: StageVillage},
		{Kind: "building", Code: "road", Stage: StageVillage},
		{Kind: "building", Code: "civic_hall", Stage: StageVillage},
		{Kind: "skill", Code: "cooking", Stage: StageVillage, Requires: &AvailabilityNeeds{}},
		{Kind: "component", Code: "timber", Stage: StageVillage, Requires: &AvailabilityNeeds{}},
		{Kind: "component", Code: "stone", Stage: StageVillage, Requires: &AvailabilityNeeds{}},
	}
	p.Availability = append(p.Availability, extra...)
}

func TestAvailabilityAcceptsACompletePack(t *testing.T) {
	p := availPack()
	tagBase(p, AvailabilityDef{Kind: "course", Code: "bread_school", Stage: StageSupport, Requires: &AvailabilityNeeds{}})
	if got := availProblems(p); got != "" {
		t.Fatalf("a complete pack was refused: %v", got)
	}
}

func TestAvailabilityRefusesAnUntaggedEntry(t *testing.T) {
	p := availPack()
	tagBase(p)
	if got := availProblems(p); !strings.Contains(got, "course/bread_school: no availability tag") {
		t.Fatalf("an untagged course was accepted: %q", got)
	}
}

func TestAvailabilityRefusesMissingPrerequisites(t *testing.T) {
	p := availPack()
	tagBase(p, AvailabilityDef{Kind: "course", Code: "bread_school", Stage: StageSupport})
	if got := availProblems(p); !strings.Contains(got, "no prerequisites declared") {
		t.Fatalf("a tag without requires was accepted: %q", got)
	}
}

func TestAvailabilityRefusesAnUndecidedStageWithoutAQuestion(t *testing.T) {
	p := availPack()
	tagBase(p, AvailabilityDef{Kind: "course", Code: "bread_school", Stage: StageUndecided, Requires: &AvailabilityNeeds{}})
	if got := availProblems(p); !strings.Contains(got, "needs a question") {
		t.Fatalf("an undecided stage without a question was accepted: %q", got)
	}
}

func TestAvailabilityRefusesABuildingWhoseStageContradictsItsTier(t *testing.T) {
	p := availPack()
	p.SettlementBuildings[0].Tier = 2
	tagBase(p, AvailabilityDef{Kind: "course", Code: "bread_school", Stage: StageSupport, Requires: &AvailabilityNeeds{}})
	if got := availProblems(p); !strings.Contains(got, "contradicts tier") {
		t.Fatalf("a stage that contradicts the tier was accepted: %q", got)
	}
}

func TestAvailabilityRefusesADeadEnd(t *testing.T) {
	// A village-only course that needs a literate teacher, with no source of
	// literacy anywhere and no Support route: nobody can ever take it.
	p := availPack()
	p.StaffRoles = []StaffRoleDef{{Code: "teacher", Personal: []AvailabilityPersonal{{Kind: PersonalLiteracy}}}}
	tagBase(p, AvailabilityDef{Kind: "course", Code: "bread_school", Stage: StageVillage,
		Requires: &AvailabilityNeeds{Staff: []string{"teacher"}}})
	if got := availProblems(p); !strings.Contains(got, "a dead end") {
		t.Fatalf("an unreachable course was accepted: %q", got)
	}
	// A Support source makes the same course reachable.
	p.Availability[len(p.Availability)-1].Elsewhere = []AvailabilityElsewhere{{Where: "support"}}
	p.PersonalSources = []PersonalSourceDef{{Kind: PersonalLiteracy, Support: true}}
	if got := availProblems(p); got != "" {
		t.Fatalf("a Support route was refused: %v", got)
	}
}

func TestAvailabilityRefusesAnUnknownReference(t *testing.T) {
	p := availPack()
	tagBase(p, AvailabilityDef{Kind: "course", Code: "bread_school", Stage: StageTown,
		Requires: &AvailabilityNeeds{Knowledge: []string{"no_such_knowledge"}, Buildings: []AvailabilityBuilding{{Code: "no_such_building"}}}})
	got := availProblems(p)
	if !strings.Contains(got, `knowledge "no_such_knowledge" does not exist`) || !strings.Contains(got, `building "no_such_building" does not exist`) {
		t.Fatalf("unknown references were accepted: %q", got)
	}
}
