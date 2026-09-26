package screens

import (
	"sort"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/commands"
	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/telegram/i18n"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// The navigation audit.
//
// A screen is layout the game core never sees: nothing here stops a screen
// from pointing a button at a command that used to exist, or from sending
// "Back" to a different place on different renders of what is meant to be
// the same screen. Both are exactly the bug the owner reported — a Back or
// an Exit that lands somewhere unrelated — and neither shows up in the
// snapshot goldens, which only prove the TEXT is stable, not that the
// GRAPH is sound.
//
// This test walks every screen every area function in this package renders
// (snapshotAreas — the same fixtures TestScreenSnapshots renders to
// testdata/snapshots), in both shipped languages, and checks the button
// graph itself:
//
//  1. every callback button names a command the game still serves to
//     players (commands.FromPlayerCommand) — a button pointing at a removed
//     or misspelled command is a dead button long before anyone presses it;
//  2. within one screen — its title up to " · ", e.g. "Bank" for both
//     "Bank · in a city with a withdrawal fee" and "Bank · in jail" — every
//     sample that carries a button labelled exactly the catalogue's
//     "button.back" sends it to the SAME command, with one named exception:
//     a "done" or "receipt" screen (a payment sent, a case filed, a job
//     quit) is deliberately allowed to send Back to the profile itself
//     regardless of what its in-flow siblings under the same title do,
//     because the flow it was part of is now over. It always ALSO offers a
//     direct button to the place the flow happened, so nothing is lost by
//     going home instead. Two DIFFERENT non-profile targets under one title
//     is still a failure: that is the bug the owner reported, a screen
//     whose logical parent depends on which sample rendered it.
//
// What this deliberately leaves to the tests that already own it:
//   - whether every button's command is well-formed and non-empty
//     (screentest.Problems, run by TestScreenSnapshots on the same
//     fixtures, and keyboards.Builder's own drop-invalid rule);
//   - group-vs-private channel rules (configs/commands.yml): enforced once,
//     centrally, by internal/gateway/groups.ForGroup for every screen alike
//     (a screen legitimately mixes group-only and private-only buttons in
//     one keyboard — a hub shows both "crime" and "bank" — so auditing that
//     per screen would flag correct screens, not broken ones), and that
//     every command has a line at all is internal/gateway/groups'
//     TestPolicyCoversEveryCommand-shaped test, not this one.
//
// Known gap: a handful of screens give their own wording to a domain's
// back button instead of the catalogue's "button.back" (election.button.back,
// "🗳 Back to the election") to say more precisely where it goes. This test
// does not recognise those as back buttons, so it does not check them; they
// are also in the areas coordination has this agent leave for later
// (elections/governance election law).
//
// Coordination: production, companies, military, war and defence (recruit
// and staging are the companies area's hiring and manufacturing flows) were
// being rewritten by other agents when this audit was written and merged to
// main only after it (1b908a3); elections, governance and diplomacy belong
// to the election-law agent. The dead-button check above still runs on every
// area without exception — a removed command is a removed command regardless
// of who owns the screen — but the back-consistency check logs, rather than
// fails, a family in deferredAreas: those screens have not been read closely
// enough yet to tell a real bug from one more multi-step wizard (see
// knownVariants below for what that looks like once it HAS been read). Spot
// checks while writing this test (military's Station wizard, several of
// production's confirm steps) found the same shape every verified case did:
// legitimate, not a bug. Re-run with deferredAreas emptied after rebasing
// onto the merged work and onto election law, and fix or allowlist whatever
// it still reports.
var deferredAreas = map[string]bool{
	"companies": true, "production": true, "recruit": true, "staging": true,
	"military": true, "war": true,
	"elections": true, "governance": true, "diplomacy": true, "appointments": true,
}

// knownVariants are families whose back button was read line by line and
// found to legitimately answer to more than one parent, because the family
// name (title prefix) is shared by more than one screen function, or one
// function itself is reached from more than one place and correctly returns
// to whichever it came from. Each is one line so the reason is checked in
// alongside the exception.
var knownVariants = map[string]string{
	// LoanConfirm (a wizard step: back to its own previous step, the specific
	// offer) and LoanDetail (a list item: back to the loan hub) both title
	// their samples "Loan · ...".
	"finance/Loan": "LoanConfirm backs to its own offer step; LoanDetail backs to the loan hub",
	// Insurance (the hub: back to the finance hub) and InsureConfirm (back to
	// the insurance hub) both title their samples "Insurance · ...".
	"finance/Insurance": "Insurance backs to the finance hub; InsureConfirm backs to Insurance",
	// History backs to the viewer's own life card when it is their own
	// history, and to the OTHER player's card when it is somebody else's —
	// both are exactly where that render was reached from.
	"life/History": "back is the life card the history belongs to: mine, or theirs",
	// LifeRefusal returns to whichever step failed (the bio prompt, the
	// avatar picker, or the search) rather than to one fixed parent.
	"life/Refused": "back is whichever step (bio, avatar, search) the refusal happened in",
	// Mission is shown both from the city's mission board (browsing) and
	// from the player's own mission list (deciding whether to abandon one
	// already accepted); back returns to whichever it was.
	"missions/Mission": "back is the board when browsing, mine when reviewing an accepted mission",
	// PropertyRefusal defaults to the player's own property list, but a
	// caller may override it (View.Back) to the specific listing the
	// refusal happened on; only the "price above the ceiling" refusal does,
	// to send the player back to adjust the offer rather than to the list.
	"property/Refused": "PropertyRefusal defaults to the player's own list; one refusal (the price ceiling) overrides it to the specific listing",
	// AuctionDetail (an item up for auction: back to the auction house),
	// AuctionNew (starting one: back to the inventory item it is made from)
	// and AuctionOpened (done: back to the auction house) all title their
	// samples "Auction · ...".
	"trade/Auction": "AuctionDetail and AuctionOpened back to the auction house; AuctionNew backs to the inventory item it lists",
}

func TestNavigationAudit(t *testing.T) {
	cat := catalogue(t)
	for _, lang := range cat.Languages() {
		t.Run(lang, func(t *testing.T) {
			auditNavigation(t, cat, lang)
		})
	}
}

type navSample struct {
	area, title string
	resp        *presenter.Response
}

func auditNavigation(t *testing.T, cat *i18n.Catalog, lang string) {
	t.Helper()
	who, ok := cast[lang]
	if !ok {
		t.Fatalf("no sample cast for the %s locale", lang)
	}
	c := Context{Msgs: cat, Lang: lang, MessageID: 42, Zone: snapshotZone}
	backLabel := c.T("button.back", nil)

	var samples []navSample
	for area, render := range snapshotAreas {
		render(c, who, func(title string, resp *presenter.Response) {
			samples = append(samples, navSample{area: area, title: title, resp: resp})
		})
	}
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].area != samples[j].area {
			return samples[i].area < samples[j].area
		}
		return samples[i].title < samples[j].title
	})

	// family collapses a sample to the screen it names: "Bank · in jail" and
	// "Bank · not in any city" are both the bank area's "Bank". The area is
	// part of the key because a title word alone is not: "Refusal", "Board"
	// and "Notice" each name a different screen in nearly every area, and
	// collapsing them together would blame one screen for another's
	// deliberately different parent.
	family := func(area, title string) string {
		if i := strings.Index(title, " · "); i >= 0 {
			title = title[:i]
		}
		return area + "/" + title
	}

	backTargets := map[string]map[string]bool{}
	for _, s := range samples {
		if s.resp == nil || s.resp.Keyboard == nil {
			continue
		}
		fam := family(s.area, s.title)
		for _, row := range s.resp.Keyboard.Rows {
			for _, b := range row {
				if b.URL != "" || b.CallbackData == "" {
					// A link button (or one with neither) carries no game
					// command; nothing here to check it against.
					continue
				}
				cmd := groups.CallbackCommand(b.CallbackData)
				if cmd == "" {
					t.Errorf("%s: %q's button %q has callback data %q that does not parse to a command",
						s.area, s.title, b.Text, b.CallbackData)
					continue
				}
				if !commands.FromPlayerCommand(cmd) {
					t.Errorf("%s: %q's button %q -> %q: not a command the game serves to players "+
						"(dead button, or a removed command left behind)", s.area, s.title, b.Text, cmd)
				}
				if b.Text == backLabel {
					if backTargets[fam] == nil {
						backTargets[fam] = map[string]bool{}
					}
					backTargets[fam][cmd] = true
				}
			}
		}
	}

	families := make([]string, 0, len(backTargets))
	for fam := range backTargets {
		families = append(families, fam)
	}
	sort.Strings(families)
	for _, fam := range families {
		targets := backTargets[fam]
		if len(targets) <= 1 {
			continue
		}
		// A "done" screen going home instead of back into a finished flow is
		// not the bug this test is for (see the doc comment); only two
		// distinct NON-home targets under one title is.
		nonHome := make(map[string]bool, len(targets))
		for tgt := range targets {
			if tgt != homeCommand {
				nonHome[tgt] = true
			}
		}
		if len(nonHome) <= 1 {
			continue
		}
		list := make([]string, 0, len(targets))
		for tgt := range targets {
			list = append(list, tgt)
		}
		sort.Strings(list)

		if why, ok := knownVariants[fam]; ok {
			t.Logf("%s[%s]: multiple back targets (%s) — known variant: %s", lang, fam, strings.Join(list, ", "), why)
			continue
		}
		area, _, _ := strings.Cut(fam, "/")
		if deferredAreas[area] {
			t.Logf("%s[%s]: multiple back targets (%s) — not yet audited, see deferredAreas",
				lang, fam, strings.Join(list, ", "))
			continue
		}
		t.Errorf("%s[%s]: %q's back button does not lead to one consistent parent across its own "+
			"screens; seen: %s", lang, fam, fam, strings.Join(list, ", "))
	}
}

// homeCommand is the dotted form of AddrHome ("player:profile.get"), the one
// address every screen's own doc comment (screens.go) already calls the
// exception to "every screen has a back button": the profile is home, so a
// "done" screen sending Back there is sending it to the top, not to a
// sibling's parent.
const homeCommand = "player.profile.get"
