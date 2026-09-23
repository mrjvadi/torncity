package groups

import (
	"reflect"
	"testing"

	"github.com/mrjvadi/torncity/internal/gateway/routing"
)

// "/pay 5000 cash" sent as a reply pays the person replied to. The words are
// read through the real router first, so a change to how routing names
// bank.pay's arguments fails here rather than paying the wrong field.
func TestPayAsReplyAimsAtTheRepliedToPlayer(t *testing.T) {
	cases := []struct {
		text string
		want map[string]any
	}{
		{"/pay 5000 cash", map[string]any{"player": "p-2", "amount": "5000", "method": "cash"}},
		{"/pay 5000", map[string]any{"player": "p-2", "amount": "5000"}},
		{"/pay", map[string]any{"player": "p-2"}},
		// An explicit payee wins over the reply.
		{"/pay @ali 5000 card", map[string]any{"to": "@ali", "amount": "5000", "method": "card"}},
	}
	for _, tc := range cases {
		command, payload, err := routing.ParseText(tc.text)
		if err != nil {
			t.Fatalf("%s: %v", tc.text, err)
		}
		if command != "bank.pay" {
			t.Fatalf("%s routed to %s", tc.text, command)
		}
		got := AimAtReply(command, payload, "p-2")
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: payload %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestAimAtReplyLeavesOtherCommandsAlone(t *testing.T) {
	payload := map[string]any{"page": "2"}
	if got := AimAtReply("map.list", payload, "p-2"); !reflect.DeepEqual(got, payload) {
		t.Errorf("map.list aimed: %v", got)
	}
	pay := map[string]any{"to": "5000"}
	if got := AimAtReply("bank.pay", pay, ""); !reflect.DeepEqual(got, pay) {
		t.Errorf("aimed with no replied-to player: %v", got)
	}
}
