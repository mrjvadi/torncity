package presence

import "testing"

func TestResolve(t *testing.T) {
	base := Input{Target: Everyone, Viewer: Everyone, Online: true, Activity: Working, Place: "market"}
	cases := []struct {
		name string
		mod  func(*Input)
		want Detail
	}{
		{"self sees all", func(i *Input) { i.Relation = Self; i.Viewer = Nobody; i.Target = Nobody },
			Detail{Visible: true, Online: true, Activity: Working, Place: "market"}},
		{"settlement mate sees all", func(i *Input) { i.Relation = Settlement },
			Detail{Visible: true, Online: true, Activity: Working, Place: "market"}},
		{"settlement mate sees a nobody target (owner default)", func(i *Input) { i.Relation = Settlement; i.Target = Nobody },
			Detail{Visible: true, Online: true, Activity: Working, Place: "market"}},
		{"nobody viewer sees nothing even in the settlement", func(i *Input) { i.Relation = Settlement; i.Viewer = Nobody },
			Detail{}},
		{"contact sees status and activity but no place", func(i *Input) { i.Relation = Contact },
			Detail{Visible: true, Online: true, Activity: Working}},
		{"contact of a nobody target sees nothing", func(i *Input) { i.Relation = Contact; i.Target = Nobody }, Detail{}},
		{"contact of a contacts target sees status", func(i *Input) { i.Relation = Contact; i.Target = Contacts },
			Detail{Visible: true, Online: true, Activity: Working}},
		{"citizen sees online only", func(i *Input) { i.Relation = Citizen },
			Detail{Visible: true, Online: true}},
		{"citizen of a contacts target sees nothing", func(i *Input) { i.Relation = Citizen; i.Target = Contacts }, Detail{}},
		{"stranger sees nothing", func(i *Input) { i.Relation = Stranger }, Detail{}},
		{"offline carries no activity or place", func(i *Input) { i.Relation = Settlement; i.Online = false },
			Detail{Visible: true}},
		{"online with no open action reads idle", func(i *Input) { i.Relation = Settlement; i.Activity = "" },
			Detail{Visible: true, Online: true, Activity: Idle, Place: "market"}},
	}
	for _, c := range cases {
		in := base
		c.mod(&in)
		if got := Resolve(in); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestStrongestAndActivityFor(t *testing.T) {
	if Strongest(nil) != Idle {
		t.Fatal("no actions is idle")
	}
	if got := Strongest([]Activity{Working, Jail, Travelling}); got != Jail {
		t.Fatalf("got %s", got)
	}
	if a, ok := ActivityFor("travel"); !ok || a != Travelling {
		t.Fatal("travel")
	}
	if _, ok := ActivityFor("market_expiry"); ok {
		t.Fatal("a market expiry is nothing a player is doing")
	}
}

func TestParse(t *testing.T) {
	for _, v := range Visibilities {
		if got, ok := Parse(string(v)); !ok || got != v {
			t.Fatalf("%s", v)
		}
	}
	if _, ok := Parse("friends"); ok {
		t.Fatal("unknown accepted")
	}
}
