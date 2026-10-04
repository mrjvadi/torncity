package clientapi

import (
	"encoding/json"
	"testing"

	"github.com/mrjvadi/torncity/internal/application/handlers"
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

// An office's grants sent as objects survive the client path: clientAlias turns them
// into strings, NormalizeArgs keeps the list, and the handler's request reads them.
func TestCharterSaveArgsReachTheHandlerRequest(t *testing.T) {
	cmd, args := clientAlias("settlement.charter.office.save", map[string]json.RawMessage{
		"office": json.RawMessage(`"founder"`), "title": json.RawMessage(`"کدخدا"`), "seats": json.RawMessage(`2`),
		"grants": json.RawMessage(`[{"permission":"road.draw"},{"permission":"treasury.spend","limit":500}]`),
	})
	if cmd != "settlement.charter.office.save" {
		t.Fatal(cmd)
	}
	payload, err := NormalizeArgs(args)
	if err != nil {
		t.Fatalf("the client path refused the grants: %v", err)
	}
	b, _ := json.Marshal(payload)
	var req handlers.VillageCharterRequest
	if err := json.Unmarshal(b, &req); err != nil {
		t.Fatalf("the handler could not read %s: %v", b, err)
	}
	if req.Office != "founder" || req.Title != "کدخدا" || req.Seats != 2 || len(req.Grants) != 2 ||
		req.Grants[0].Permission != "road.draw" || req.Grants[1].Permission != "treasury.spend" || req.Grants[1].Limit != 500 {
		t.Fatalf("decoded %+v", req)
	}
	// strings in the same form (the Telegram edge) read the same
	if err := json.Unmarshal([]byte(`{"seats":"3","term_days":"7","grants":["road.draw","treasury.spend:900"]}`), &req); err != nil || req.Seats != 3 || req.TermDays != 7 || req.Grants[1].Limit != 900 {
		t.Fatalf("string form: %+v %v", req, err)
	}
}
