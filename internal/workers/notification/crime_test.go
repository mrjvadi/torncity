package notification

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func crimeRoute(t *testing.T, event string) Route {
	t.Helper()
	for _, r := range Routes() {
		if r.Domain == "crime" && r.Event == event {
			return r
		}
	}
	t.Fatalf("no crime.%s route", event)
	return Route{}
}

// Every crime notice reaches the one player it is about, privately, in their
// language, naming crimes, venues and cities by name — never by a code — and
// the victim of a theft learns the thief only when a witness saw them.
func TestCrimeNoticesGoToThePlayerTheyAreAbout(t *testing.T) {
	cases := []struct {
		event   string
		payload map[string]any
		want    []string
		absent  []string
	}{
		{"victimised", map[string]any{
			"victim_id": playerID, "attempt_id": "a1", "crime": "pickpocketing", "crime_name": "Pickpocketing",
			"venue": "train_station", "venue_name": "Train station", "city_code": "ostmarch", "city_name": "Ostmarch",
			"amount": 1500, "report_fee": 200, "report_window_seconds": 86400,
		}, []string{"Pickpocketing", "1,500", "report it to the police"}, []string{"pickpocketing", "train_station", "A witness"}},
		{"victimised", map[string]any{
			"victim_id": playerID, "attempt_id": "a1", "crime": "pickpocketing", "crime_name": "Pickpocketing",
			"venue": "train_station", "venue_name": "Train station", "city_code": "ostmarch", "city_name": "Ostmarch",
			"amount": 1500, "report_fee": 200, "report_window_seconds": 86400, "thief_name": "Kaveh", "thief_code": "B3C4D5F",
		}, []string{"Kaveh", "B3C4D5F"}, nil},
		{"take", map[string]any{
			"player_id": playerID, "crime": "pickpocketing", "crime_name": "Pickpocketing", "venue": "train_station",
			"venue_name": "Train station", "city_code": "ostmarch", "city_name": "Ostmarch", "result": "succeeded",
			"victim_player": true, "take": 1500, "heat": 3, "heat_max": 100, "wanted": 1, "stars": 5, "nerve": 18, "nerve_max": 20,
		}, []string{"1,500"}, []string{"pickpocketing"}},
		{"resolved", map[string]any{
			"player_id": playerID, "crime": "home_burglary", "crime_name": "Home burglary", "venue": "city_centre",
			"venue_name": "City centre", "city_code": "ostmarch", "city_name": "Ostmarch", "result": "caught",
			"fine": 500, "fine_paid": 500, "jail_seconds": 480, "heat": 18, "heat_max": 100, "wanted": 1, "stars": 5,
		}, []string{"Home burglary", "8m"}, []string{"home_burglary"}},
		{"released", map[string]any{"player_id": playerID, "city_code": "ostmarch", "city_name": "Ostmarch"}, nil, nil},
		{"case_solved", map[string]any{
			"victim_id": playerID, "thief_id": "t1", "thief_name": "Kaveh", "thief_code": "B3C4D5F", "crime": "pickpocketing",
			"crime_name": "Pickpocketing", "city_code": "ostmarch", "city_name": "Ostmarch", "stolen": 1500, "restored": 1100, "shortfall": 400,
		}, []string{"Kaveh", "1,100", "400"}, nil},
		{"case_closed", map[string]any{
			"victim_id": playerID, "crime": "pickpocketing", "crime_name": "Pickpocketing", "city_code": "ostmarch", "city_name": "Ostmarch",
		}, nil, []string{"Kaveh"}},
		{"convicted", map[string]any{
			"victim_id": "v1", "thief_id": playerID, "thief_name": "Kaveh", "crime": "pickpocketing", "crime_name": "Pickpocketing",
			"city_code": "ostmarch", "city_name": "Ostmarch", "restored": 1100, "shortfall": 400, "fine": 300, "fine_paid": 0,
			"term_seconds": 240,
		}, []string{"1,100", "4m"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.event, func(t *testing.T) {
			p := englishPlayer()
			r := newRig(t, p, link(botA, 1001))
			env := arrivalEvent(t, "req-"+tc.event, r.now, nil)
			env.Metadata.Command = "crime." + tc.event
			raw, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatal(err)
			}
			env.Payload = raw
			if err := r.w.Handle(context.Background(), crimeRoute(t, tc.event), env); err != nil {
				t.Fatal(err)
			}
			if len(r.sender.sent) != 1 {
				t.Fatalf("sent %d notices, want 1", len(r.sender.sent))
			}
			resp := r.sender.sent[0].notice.Response
			if !resp.Private {
				t.Error("a crime notice is not marked private")
			}
			for _, w := range tc.want {
				if !strings.Contains(resp.Text, w) {
					t.Errorf("notice %q lacks %q", resp.Text, w)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(resp.Text, a) {
					t.Errorf("notice %q shows %q", resp.Text, a)
				}
			}
			if strings.Contains(resp.Text, "crime.") {
				t.Errorf("notice shows a catalogue key: %q", resp.Text)
			}
		})
	}
}
