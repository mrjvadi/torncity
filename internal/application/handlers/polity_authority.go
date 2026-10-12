package handlers

import (
	"context"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/charter"
	"github.com/mrjvadi/torncity/internal/domain/polity"
)

// The authority of a player country (docs/adr/0076 8 and 9). A content country (the two in governance.yml) is run by its fixed offices as
// before; a country the players founded is run by the offices they wrote in its charter, and an act of the country asks for the permission
// that stands in place of the old office. The handlers of treaties, sanctions, war and the forces call mayAct and cleared: both ask here
// first, so none of them changes.

// actionPermissions are the permissions that stand behind an action of governance.yml, any one of which lets the holder take it. The
// treaty action covers proposing, answering and ending (the old foreign minister did all three); war covers declaring, joining and making
// peace.
func actionPermissions(action string) []charter.Permission {
	switch action {
	case content.ActionTreaty:
		return []charter.Permission{charter.TreatySign, charter.TreatyPropose, charter.TreatyEnd}
	case content.ActionSanction:
		return []charter.Permission{charter.SanctionImpose}
	case content.ActionWar:
		return []charter.Permission{charter.WarDeclare, charter.WarPeace}
	case content.ActionProcure:
		return []charter.Permission{charter.MilitaryProcure}
	case content.ActionDefenceLicence:
		return []charter.Permission{charter.MilitaryLicence}
	case "country.command_ground", "country.command_air", "country.command_navy", "country.command_air_defence":
		return []charter.Permission{charter.MilitaryCommand}
	}
	return nil
}

// playerCountry is the union behind a country jurisdiction, nil for a content country.
func playerCountry(ctx context.Context, tx application.Tx, countryID string) (*application.Union, error) {
	return tx.Unions().CountryByJurisdiction(ctx, countryID)
}

// mayActUnion answers whether the player holds, in the country's charter, a permission that stands behind the action. It returns the
// office acted from (the title of the first of the player's offices that holds the permission).
func mayActUnion(ctx context.Context, tx application.Tx, u *application.Union, action string, p *application.Player) (application.Office, bool, error) {
	perms := actionPermissions(action)
	if len(perms) == 0 {
		return application.Office{}, false, nil
	}
	offices, err := tx.Unions().OfficesOf(ctx, u.ID, p.ID)
	if err != nil {
		return application.Office{}, false, err
	}
	for _, o := range offices {
		for _, g := range o.Grants {
			for _, want := range perms {
				if g.Permission != want {
					continue
				}
				if polity.Ceiling(u.Delegations, []charter.Grant{g}) != nil {
					continue
				}
				return application.Office{OfficeCode: o.Title, JurisdictionID: u.JurisdictionID, HolderPlayerID: p.ID}, true, nil
			}
		}
	}
	return application.Office{}, false, nil
}

// clearedUnion says whether the player holds a power over the forces or the war in the country's charter: they may see its forces in full.
func clearedUnion(ctx context.Context, tx application.Tx, u *application.Union, p *application.Player) (bool, error) {
	held, err := tx.Unions().HeldIn(ctx, u.ID, p.ID)
	if err != nil {
		return false, err
	}
	for _, perm := range []charter.Permission{charter.MilitaryCommand, charter.MilitaryProcure, charter.MilitaryLicence, charter.WarDeclare, charter.WarPeace} {
		if _, ok := held.Has(perm); ok {
			return true, nil
		}
	}
	return false, nil
}
