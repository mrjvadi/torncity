package handlers

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/presentation"
)

func villageHere(owned []string, built ...content.AvailabilityBuilding) courseHere {
	c := courseHere{tier: "village", stage: content.StageRank("village"), owned: map[string]bool{}}
	for _, k := range owned {
		c.owned[k] = true
	}
	c.stands = func(b content.AvailabilityBuilding) bool {
		for _, have := range built {
			if have.Code == b.Code && have.Role == b.Role && have.Tier >= b.Tier {
				return true
			}
		}
		return false
	}
	return c
}

func TestAContentCityTeachesEveryCourse(t *testing.T) {
	taught, reach, needs := courseHere{all: true}.judge(shippedSnapshot(t), content.AvailabilityDef{Stage: "city"}, true)
	if !taught || !reach || len(needs) != 0 {
		t.Errorf("got %v %v %v, want everything taught", taught, reach, needs)
	}
}

func TestAVillageDoesNotTeachATownCourseAndDoesNotMentionIt(t *testing.T) {
	tag := content.AvailabilityDef{Kind: "course", Code: "driving_licence", Stage: "town"}
	taught, reach, needs := villageHere(nil).judge(shippedSnapshot(t), tag, true)
	if taught || reach || len(needs) != 1 || needs[0].Kind != presentation.CourseNeedStage {
		t.Errorf("got %v %v %v, want not taught, out of reach, asking for the stage", taught, reach, needs)
	}
}

func TestAVillageCourseNamesWhatIsMissing(t *testing.T) {
	tag := content.AvailabilityDef{Kind: "course", Code: "first_aid", Stage: "village", Requires: &content.AvailabilityNeeds{
		Knowledge: []string{"basic_medicine"}, Buildings: []content.AvailabilityBuilding{{Role: "health", Tier: 1}}}}
	taught, reach, needs := villageHere(nil).judge(shippedSnapshot(t), tag, true)
	if taught || !reach || len(needs) != 2 {
		t.Fatalf("got %v %v %v, want a reachable course missing knowledge and a building", taught, reach, needs)
	}
	if needs[0].Kind != presentation.CourseNeedKnowledge || needs[0].Code != "basic_medicine" ||
		needs[1].Kind != presentation.CourseNeedBuilding || needs[1].Role != "health" {
		t.Errorf("needs: %+v", needs)
	}
	here := villageHere([]string{"basic_medicine"}, content.AvailabilityBuilding{Role: "health", Tier: 1})
	if taught, _, needs := here.judge(shippedSnapshot(t), tag, true); !taught || len(needs) != 0 {
		t.Errorf("with both it must be taught, got %v %v", taught, needs)
	}
}

func TestACourseWithATeacherNeedsTheClassBuilding(t *testing.T) {
	snap := shippedSnapshot(t)
	tag, ok := snap.AvailabilityTag("course", "bookkeeping")
	if !ok {
		t.Fatal("bookkeeping has no tag")
	}
	town := villageHere([]string{"record_keeping"})
	town.tier, town.stage = "town", content.StageRank("town")
	taught, _, needs := town.judge(snap, tag, true)
	if taught || len(needs) == 0 {
		t.Fatalf("a town with no school must not teach bookkeeping: %v %v", taught, needs)
	}
	teacher := false
	for _, n := range needs {
		teacher = teacher || n.Kind == presentation.CourseNeedBuilding
	}
	if !teacher {
		t.Errorf("the missing school must be named, got %+v", needs)
	}
}

func TestStageReachesReadsTheTags(t *testing.T) {
	snap := shippedSnapshot(t)
	for _, c := range []struct {
		kind, code, tier string
		want             bool
	}{
		{"mission_board", "village_works", "village", true},
		{"mission_board", "city_hall", "village", false},
		{"mission_board", "police", "town", false},
		{"mission", "village_first_lesson", "village", true},
		{"mission", "first_steps", "village", false},
	} {
		if got := stageReaches(snap, c.kind, c.code, c.tier); got != c.want {
			t.Errorf("%s %s at %s: got %v, want %v", c.kind, c.code, c.tier, got, c.want)
		}
	}
}

func TestKnowledgeUnlocksNamesBuildingsAndCourses(t *testing.T) {
	snap := shippedSnapshot(t)
	d, ok := snap.SettlementKnowledgeDef("basic_medicine")
	if !ok {
		t.Fatal("no basic_medicine")
	}
	got := map[string]bool{}
	for _, u := range knowledgeUnlocks(snap, d) {
		got[u.Kind+"/"+u.Item.Code] = true
	}
	if !got["course/first_aid"] {
		t.Errorf("basic medicine must open the first aid course: %v", got)
	}
}
