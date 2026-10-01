package content

import "testing"

// TestServiceTagsOfTheShippedWorld: the services a player may be refused
// "not available here" are tagged in the shipped availability.yml: each
// finance service by itself, the auction house through the place that has it.
func TestServiceTagsOfTheShippedWorld(t *testing.T) {
	pack, err := Load(shippedContentDir(t))
	if err != nil {
		t.Fatal(err)
	}
	snap, err := BuildSnapshot(1, pack)
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range []string{"loans", "savings", "insurance", "stocks", "gold", "auction_house"} {
		tag, ok := snap.ServiceTag(service)
		if !ok {
			t.Errorf("%s: no availability tag says where it is offered", service)
			continue
		}
		if StageRank(tag.Stage) == 0 && tag.Stage != StageSupport {
			t.Errorf("%s: stage %q cannot be judged", service, tag.Stage)
		}
		if len(tag.Elsewhere) == 0 {
			t.Errorf("%s: the tag names no place to have it when it is missing here", service)
		}
	}
	if _, ok := snap.ServiceTag("a_service_nothing_tags"); ok {
		t.Error("an untagged service must be offered everywhere")
	}
	if StageRank(StageVillage) >= StageRank(StageTown) || StageRank(StageTown) >= StageRank(StageCity) {
		t.Error("stages must order village < town < city")
	}
}
