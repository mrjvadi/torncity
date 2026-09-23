package screens

import (
	"strings"
	"testing"
)

// In a group (Context.Shared) the profile and the travel choice leave the
// player's money out and show everything else; in a private chat they show
// it. The screens that exist to state the player's cash are private instead.
func TestSharedScreensLeaveMoneyOut(t *testing.T) {
	for _, shared := range []bool{false, true} {
		c := ctx(t, "en", 0)
		c.Shared = shared
		cash := c.T("profile.cash", map[string]any{"cash": FormatMoney(c, 1_234_567)})

		profile := Profile(c, ProfileView{Name: "Ada", Level: 3, Cash: 1_234_567, Bank: 7_654_321}).Text
		if got := strings.Contains(profile, cash); got == shared {
			t.Errorf("shared=%v: profile shows cash = %v\n%s", shared, got, profile)
		}
		if !strings.Contains(profile, "Ada") {
			t.Errorf("shared=%v: the profile lost the player's name\n%s", shared, profile)
		}

		travelCash := c.T("travel.cash", map[string]any{"cash": FormatMoney(c, 1_234_567)})
		options := TravelOptions(c, TravelOptionsView{ToCode: "x", To: "X", Cash: 1_234_567}).Text
		if got := strings.Contains(options, travelCash); got == shared {
			t.Errorf("shared=%v: travel options show cash = %v\n%s", shared, got, options)
		}
	}

	c := ctx(t, "en", 0)
	if !TravelNoFunds(c, TravelFundsView{ToCode: "x", Fare: 10, Cash: 5}).Private {
		t.Error("the no-funds screen states the player's cash and is not private")
	}
	if !Refusal(c, RefusalView{Kind: RefusalCannotAfford, Fee: 10, Cash: 5}).Private {
		t.Error("the cannot-afford refusal states the player's cash and is not private")
	}
	if Refusal(c, RefusalView{Kind: RefusalNotEmployed}).Private {
		t.Error("a refusal with no money in it is private")
	}
}
