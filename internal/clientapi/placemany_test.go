package clientapi

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPlaceManyArgsSpellsLotsAsTokens(t *testing.T) {
	args := map[string]json.RawMessage{
		"code": json.RawMessage(`"road"`),
		"lots": json.RawMessage(`[{"x":1,"y":2},{"x":3,"y":0,"rotated":true}]`),
	}
	cmd, out := clientAlias("settlement.build.place_many", args)
	if cmd != "settlement.build.place_many" {
		t.Fatalf("command = %q", cmd)
	}
	payload, err := NormalizeArgs(out)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := payload["lots"], []string{"1-2", "3-0-r"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lots = %v, want %v", got, want)
	}
	if payload["code"] != "road" {
		t.Errorf("code = %v", payload["code"])
	}
}

func TestPlaceManyArgsLeavesTokensAndLinesAlone(t *testing.T) {
	args := map[string]json.RawMessage{
		"lots": json.RawMessage(`["1-1","2-1"]`),
	}
	_, out := clientAlias("settlement.build.place_many", args)
	payload, err := NormalizeArgs(out)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := payload["lots"], []string{"1-1", "2-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lots = %v, want %v", got, want)
	}
}

func TestListArgumentsAllowABatchOfTwentyFiveLots(t *testing.T) {
	lots := make([]string, 25)
	for i := range lots {
		lots[i] = `"1-1"`
	}
	raw := json.RawMessage("[" + joinComma(lots) + "]")
	if _, err := NormalizeArgs(map[string]json.RawMessage{"lots": raw}); err != nil {
		t.Errorf("a batch of 25 lots was refused: %v", err)
	}
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}
