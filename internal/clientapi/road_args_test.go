package clientapi

import (
	"encoding/json"
	"testing"
)

func TestRoadPlanArgsNameTheEndByNumbers(t *testing.T) {
	cmd, args := clientAlias("settlement.road.plan", map[string]json.RawMessage{
		"x": json.RawMessage(`-14`), "y": json.RawMessage(`19`), "from_x": json.RawMessage(`2`), "from_y": json.RawMessage(`-3`),
		"class": json.RawMessage(`"path"`), "confirm": json.RawMessage(`"confirm"`),
	})
	if cmd != "settlement.road.plan" || string(args["to"]) != `"m14-19"` || string(args["from"]) != `"2-m3"` ||
		args["x"] != nil || args["y"] != nil || args["from_x"] != nil || string(args["class"]) != `"path"` || string(args["confirm"]) != `"confirm"` {
		t.Fatalf("road plan args = %s %v", cmd, args)
	}
	// a token that is already there is left alone
	_, args = clientAlias("settlement.road.plan", map[string]json.RawMessage{"to": json.RawMessage(`"3-4"`), "x": json.RawMessage(`9`), "y": json.RawMessage(`9`)})
	if string(args["to"]) != `"3-4"` {
		t.Fatalf("an explicit token was overwritten: %v", args)
	}
}
