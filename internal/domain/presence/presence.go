// Package presence is the pure half of "who is around and what are they
// doing" (docs/adr/0030-realtime-interest-and-presence.md section 3): the
// closed vocabulary of a player's activity, the privacy setting a player
// chooses, and the one function that decides how much of a player a given
// viewer may see.
//
// Everything here is a function of its arguments. Where the online flag comes
// from (a Redis key with a heartbeat), where the activity comes from (the
// player's open game_actions row) and who is whose friend are the callers'
// business; that the same rule shapes the HTTP status, the settlement player
// list, the Telegram group screen and the realtime delta is this package's.
package presence

// Visibility is the player's own "last seen" setting.
type Visibility string

const (
	// Everyone: fellow citizens (and the rows of section 3.3 below them) may
	// see the player online. The default.
	Everyone Visibility = "everyone"
	// Contacts: accepted friends and faction mates only (plus the player's
	// own settlement, see Resolve).
	Contacts Visibility = "contacts"
	// Nobody: no one outside the player's own settlement sees anything, and
	// the player sees no one else's presence.
	Nobody Visibility = "nobody"
)

// Visibilities is every setting, in the order a settings screen offers them.
var Visibilities = []Visibility{Everyone, Contacts, Nobody}

// Valid reports whether v is one of the three settings.
func (v Visibility) Valid() bool {
	switch v {
	case Everyone, Contacts, Nobody:
		return true
	}
	return false
}

// Parse reads a setting, refusing anything that is not one of the three.
func Parse(s string) (Visibility, bool) {
	v := Visibility(s)
	return v, v.Valid()
}

// Activity is what a player is doing right now: a small closed vocabulary,
// always derived from the player's open game_actions row, never reported by
// a client (ADR 0030 section 3.1).
type Activity string

// The activities. Their Persian and English labels are locale keys
// (presence.activity.<code>).
const (
	Idle       Activity = "idle"
	Travelling Activity = "travelling"
	Working    Activity = "working"
	Studying   Activity = "studying"
	Training   Activity = "training"
	Hospital   Activity = "hospital"
	Jail       Activity = "jail"
	Building   Activity = "building"
	Fighting   Activity = "fighting"
)

// activityOf maps game_actions.action_type to an activity. The string keys
// are the values the schedulers write (internal/workers/scheduler/routes.go);
// a type that is not here (a market expiry, a company period) is nothing a
// player is visibly doing, and reads as idle.
var activityOf = map[string]Activity{
	"travel":             Travelling,
	"military_move":      Travelling,
	"work_shift":         Working,
	"education":          Studying,
	"hospital_discharge": Hospital,
	"jail_release":       Jail,
	"war_operation":      Fighting,
	"faction_crime":      Fighting,
}

// ActionTypes lists the game_actions.action_type values that mean something
// a player is doing, for a query that wants only those.
func ActionTypes() []string {
	out := make([]string, 0, len(activityOf))
	for t := range activityOf {
		out = append(out, t)
	}
	return out
}

// priority orders what to show when a player has several open actions: a
// cell or a ward outranks a walk, which outranks a shift.
var priority = map[Activity]int{
	Jail: 90, Hospital: 80, Fighting: 70, Travelling: 60, Working: 50, Studying: 40, Training: 30, Building: 20, Idle: 0,
}

// ActivityFor is the activity of one open action type, and whether the type
// is one a player is visibly doing.
func ActivityFor(actionType string) (Activity, bool) {
	a, ok := activityOf[actionType]
	return a, ok
}

// Strongest picks the activity to show among the activities of a player's
// open actions. No actions is Idle.
func Strongest(open []Activity) Activity {
	best := Idle
	for _, a := range open {
		if priority[a] > priority[best] {
			best = a
		}
	}
	return best
}

// Relation is how the viewer stands to the player being looked at, computed
// server-side from residency, friendships and factions — never from
// anything a client says.
type Relation int

const (
	// Stranger: no tie the rules recognise. Sees nothing.
	Stranger Relation = iota
	// Citizen: lives under the same country. Sees online or offline only.
	Citizen
	// Contact: an accepted friend or a faction mate, anywhere. Sees status
	// and activity, never the place.
	Contact
	// Settlement: lives in, or is standing in, the same settlement. Sees
	// everything.
	Settlement
	// Self: the player themself.
	Self
)

// Input is everything Resolve needs about one viewer looking at one player.
type Input struct {
	Relation Relation
	// Target is the looked-at player's setting; Viewer is the viewer's.
	Target, Viewer Visibility
	// Online is whether the player's heartbeat key is alive; Activity what
	// their open action says (Idle for none); Place where they stand now.
	Online   bool
	Activity Activity
	Place    string
}

// Detail is what a viewer may see of a player. A field the rules withhold is
// its zero value, and Visible says whether any of it is shown at all — so a
// client can tell "offline" from "not yours to know".
type Detail struct {
	// Visible is false when presence is withheld altogether: the client
	// shows no dot at all.
	Visible  bool
	Online   bool
	Activity Activity
	Place    string
}

// Resolve applies ADR 0030 sections 3.2 and 3.3.
//
// The player's own settlement always sees them, whatever they chose (the
// owner's default of 2026-09-28: a settlement's roster is a civic fact, not a
// contact list). Beyond it, the target's setting decides which of the wider
// tiers may look: Everyone opens the citizen tier, Contacts only contacts,
// Nobody nothing. A viewer who chose Nobody sees no one's presence, the same
// trade Telegram makes; they still get a roster of names from the callers.
func Resolve(in Input) Detail {
	if in.Relation == Self {
		return full(in)
	}
	if in.Viewer == Nobody {
		return Detail{}
	}
	switch in.Relation {
	case Settlement:
		return full(in)
	case Contact:
		if in.Target == Nobody {
			return Detail{}
		}
		d := full(in)
		d.Place = ""
		return d
	case Citizen:
		if in.Target != Everyone {
			return Detail{}
		}
		return Detail{Visible: true, Online: in.Online}
	}
	return Detail{}
}

func full(in Input) Detail {
	d := Detail{Visible: true, Online: in.Online}
	if in.Online {
		d.Activity = in.Activity
		if d.Activity == "" {
			d.Activity = Idle
		}
		d.Place = in.Place
	}
	return d
}
