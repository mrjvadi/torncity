package notification

import "testing"

// shippedDeliveryModes is the file cmd/notifier actually loads. A broken
// file is fatal at startup (LoadDeliveryModes' own doc), so this is the
// build's proof that it still parses and says what this package assumes.
const shippedDeliveryModes = "../../../configs/notifications/delivery.yml"

func TestShippedDeliveryModesLoad(t *testing.T) {
	d, err := LoadDeliveryModes(shippedDeliveryModes)
	if err != nil {
		t.Fatalf("loading %s: %v", shippedDeliveryModes, err)
	}

	// The default is "inbox": the whole point of the feature is that most
	// notices no longer arrive as their own message.
	if mode, _ := d.Classify("some", "unlisted-kind"); mode != ModeInbox {
		t.Errorf("default mode is %q, want inbox", mode)
	}

	// A representative sample of the owner's urgent examples must still be
	// instant, and an ordinary report must still default to inbox.
	cases := []struct {
		domain, event string
		want          Mode
	}{
		{"health", "hospitalised", ModeInstant},
		{"crime", "victimised", ModeInstant},
		{"bank", "payment_received", ModeInstant},
		{"life", "hunger_low", ModeInstant},
		{"company", "period_settled", ModeInbox},
		{"market", "filled", ModeInbox},
	}
	for _, c := range cases {
		if mode, _ := d.Classify(c.domain, c.event); mode != c.want {
			t.Errorf("%s.%s classified %q, want %q", c.domain, c.event, mode, c.want)
		}
	}

	// Every category the file names must have a category, never empty.
	for _, domain := range []string{"company", "market", "health", "war", "life"} {
		if _, category := d.Classify(domain, "anything"); category == "" {
			t.Errorf("%s has no fallback category", domain)
		}
	}
}
