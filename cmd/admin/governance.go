package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// Operator tooling for player-held offices (docs/adr/0015-player-held-offices.md).
//
// Until elections exist, `office appoint` is how the first player mayor comes
// to be. The operator seats and unseats; the operator never sets a lever — that
// is the office holder's decision, inside the bounds the content declares.
// Every appointment and vacancy writes an audit row in the same transaction.

func officeUsage() {
	fmt.Fprint(os.Stderr, `usage: admin office <command>

  appoint --office CODE (--city CODE | --country CODE) --player PUBLIC_CODE --reason "why"
          [--seat N] [--by NAME]
                          seat a player in a vacant seat (the seat defaults to 1)
  vacate  --office CODE (--city CODE | --country CODE) --reason "why" [--seat N] [--by NAME]
                          empty a seat; values its holder set stay in force
  list    [--city CODE | --country CODE]
                          every seat and who holds it

--by names the operator in the audit row; it defaults to $`+operatorEnv+`, then
$USER, and the change is refused when none of them names anybody.

DATABASE_URL must be set.
`)
}

func policyUsage() {
	fmt.Fprint(os.Stderr, `usage: admin policy <command>

  show (--city CODE | --country CODE)
                          every lever's value in force there and above it,
                          whether it is the default or set, by whom, and who
                          may change it now

DATABASE_URL must be set.
`)
}

// officeCommand dispatches the office subcommands.
func officeCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		officeUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "appoint":
		return officeChange(ctx, args[1:], true)
	case "vacate":
		return officeChange(ctx, args[1:], false)
	case "list":
		return officeList(ctx, args[1:])
	default:
		officeUsage()
		os.Exit(2)
		return nil
	}
}

// policyCommand dispatches the policy subcommands.
func policyCommand(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "show" {
		policyUsage()
		os.Exit(2)
	}
	return policyShow(ctx, args[1:])
}

// placeFlags are the two ways an operator names a jurisdiction.
type placeFlags struct {
	city, country *string
}

func addPlaceFlags(fs *flag.FlagSet) placeFlags {
	return placeFlags{
		city:    fs.String("city", "", "city code"),
		country: fs.String("country", "", "country code"),
	}
}

// resolve returns the (kind, code) named, or ok=false when neither is given.
func (p placeFlags) resolve() (kind, code string, ok bool, err error) {
	city, country := strings.TrimSpace(*p.city), strings.TrimSpace(*p.country)
	switch {
	case city != "" && country != "":
		return "", "", false, errors.New("name one place: --city or --country, not both")
	case city != "":
		return "city", city, true, nil
	case country != "":
		return "country", country, true, nil
	}
	return "", "", false, nil
}

// officeChange appoints or vacates one seat.
func officeChange(ctx context.Context, args []string, appoint bool) error {
	name := "office vacate"
	if appoint {
		name = "office appoint"
	}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = officeUsage
	office := fs.String("office", "", "office code, e.g. mayor")
	place := addPlaceFlags(fs)
	seat := fs.Int("seat", 1, "seat number")
	player := fs.String("player", "", "the appointee's public player code")
	reason := fs.String("reason", "", "why (required)")
	operator := addOperatorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Checked before the database is dialled, like every audited command.
	if strings.TrimSpace(*reason) == "" {
		return fmt.Errorf("%s: --reason is required; an office change with no recorded reason cannot be understood later", name)
	}
	if strings.TrimSpace(*office) == "" {
		return fmt.Errorf("%s: --office is required", name)
	}
	kind, code, ok, err := place.resolve()
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if !ok {
		return fmt.Errorf("%s: name the place with --city or --country", name)
	}
	if appoint && strings.TrimSpace(*player) == "" {
		return fmt.Errorf("%s: --player is required", name)
	}
	who, err := operator.resolve(name, os.LookupEnv)
	if err != nil {
		return err
	}

	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	req := postgres.SeatChangeRequest{
		SeatRef: postgres.SeatRef{
			OfficeCode:       strings.TrimSpace(*office),
			JurisdictionKind: kind,
			JurisdictionCode: code,
			Seat:             *seat,
		},
		PlayerCode: *player,
		Actor:      who,
		Reason:     *reason,
		At:         time.Now().UTC(),
	}
	admin := postgres.NewGovernanceAdmin(pool)
	var change postgres.SeatChange
	if appoint {
		change, err = admin.Appoint(ctx, req)
	} else {
		change, err = admin.Vacate(ctx, req)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}

	where := change.Jurisdiction.Kind + " " + change.Jurisdiction.Code
	if appoint {
		fmt.Printf("appointed %s as %s (seat %d) of %s\n", change.PlayerLabel, req.OfficeCode, req.Seat, where)
		if change.After.TermEndsAt != nil {
			fmt.Printf("term ends:  %s\n", change.After.TermEndsAt.UTC().Format(time.RFC3339))
		} else {
			fmt.Println("term ends:  never (held at pleasure)")
		}
	} else {
		fmt.Printf("vacated %s (seat %d) of %s, held until now by %s\n", req.OfficeCode, req.Seat, where, change.PlayerLabel)
		fmt.Println("values the holder set stay in force; a deputy, if any, acts until the seat is filled")
	}
	fmt.Printf("seat id:    %s\n", change.After.ID)
	fmt.Printf("by:         %s\n", req.Actor)
	fmt.Printf("reason:     %s\n", strings.TrimSpace(req.Reason))
	return nil
}

// officeList prints seats and their holders.
func officeList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("office list", flag.ExitOnError)
	fs.Usage = officeUsage
	place := addPlaceFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	kind, code, ok, err := place.resolve()
	if err != nil {
		return err
	}

	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	admin := postgres.NewGovernanceAdmin(pool)

	var ids []string
	if ok {
		j, err := admin.JurisdictionByCode(ctx, kind, code)
		if err != nil {
			return err
		}
		ids = []string{j.ID}
	}
	seats, err := admin.Seats(ctx, ids)
	if err != nil {
		return err
	}
	if len(seats) == 0 {
		fmt.Println("no seats; load content with governance first (admin content load)")
		return nil
	}
	held := 0
	for _, s := range seats {
		holder := "vacant"
		if !s.Vacant() {
			held++
			holder = fmt.Sprintf("%s via %s since %s", s.HolderLabel, s.AcquiredBy, s.Since.UTC().Format(time.RFC3339))
		}
		fmt.Printf("  %-8s %-16s %-14s seat %d  %s\n", s.JurisdictionKind, s.JurisdictionCode, s.OfficeCode, s.Seat, holder)
	}
	fmt.Printf("\n%d seat(s), %d held, %d vacant\n", len(seats), held, len(seats)-held)
	return nil
}

// policyShow prints every lever in force at a place and at each jurisdiction
// above it, through the one resolver every other reader uses.
func policyShow(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("policy show", flag.ExitOnError)
	fs.Usage = policyUsage
	place := addPlaceFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	kind, code, ok, err := place.resolve()
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("policy show: name the place with --city or --country")
	}

	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	admin := postgres.NewGovernanceAdmin(pool)
	policy := postgres.NewPolicyReader(pool, nil)

	j, err := admin.JurisdictionByCode(ctx, kind, code)
	if err != nil {
		return err
	}
	ancestry, err := admin.Ancestry(ctx, j.ID)
	if err != nil {
		return err
	}
	levers, err := admin.ActiveLevers(ctx)
	if err != nil {
		return err
	}

	for _, place := range ancestry {
		fmt.Printf("%s %s (%s)\n", place.Kind, place.Code, place.Name)
		shown := 0
		for _, l := range levers {
			if l.Jurisdiction != place.Kind {
				continue
			}
			shown++
			if err := printLever(ctx, admin, policy, place, l); err != nil {
				return err
			}
		}
		if shown == 0 {
			fmt.Println("  (no levers at this level)")
		}
		fmt.Println()
	}
	return nil
}

// printLever prints one lever as the resolver answers it.
func printLever(ctx context.Context, admin *postgres.GovernanceAdmin, policy application.PolicyReader,
	place application.Jurisdiction, l application.LeverDefinition,
) error {
	fmt.Printf("  %s\n", l.Code)
	v, err := policy.Get(ctx, place.ID, l.Code)
	if errors.Is(err, application.ErrLeverKindUnsupported) {
		fmt.Printf("    %s lever: declared and stored; not readable until its behaviour exists\n", l.Type)
		return nil
	}
	if err != nil {
		return err
	}

	var ids []string
	if v.InForce != nil {
		ids = append(ids, v.InForce.SetByPlayerID)
	}
	if v.Pending != nil {
		ids = append(ids, v.Pending.SetByPlayerID)
	}
	if v.Acting != nil {
		for _, h := range v.Acting.Holders {
			ids = append(ids, h.HolderPlayerID)
		}
	}
	names, err := admin.PlayerLabels(ctx, ids)
	if err != nil {
		return err
	}

	fmt.Printf("    value:    %s  [bounds %s .. %s]\n", formatLever(l.Type, v.Value),
		formatLever(l.Type, l.Min), formatLever(l.Type, l.Max))
	switch v.Source {
	case application.PolicyFromOffice:
		fmt.Printf("    source:   set by %s, in force since %s\n",
			names[v.InForce.SetByPlayerID], v.InForce.EffectiveAt.UTC().Format(time.RFC3339))
	default:
		origin := "the lever's default"
		if l.CityDefault != "" {
			origin = "the city's own default (" + l.CityDefault + ")"
		}
		fmt.Printf("    source:   default — %s\n", origin)
	}
	if v.Clamped {
		fmt.Println("    note:     clamped into the current bounds")
	}
	if v.Pending != nil {
		fmt.Printf("    pending:  %s from %s, announced by %s\n", formatLever(l.Type, v.Pending.Value),
			v.Pending.EffectiveAt.UTC().Format(time.RFC3339), names[v.Pending.SetByPlayerID])
	}
	switch {
	case l.DecisionRule != application.DecisionSingle:
		fmt.Printf("    decided:  by a %s vote of %s (voting is not built yet)\n", l.DecisionRule, l.HeldBy)
	case v.Acting == nil:
		fmt.Printf("    decided:  by %s — vacant and no deputy acts; nobody can change it, its value stands\n", l.HeldBy)
	default:
		holders := make([]string, 0, len(v.Acting.Holders))
		for _, h := range v.Acting.Holders {
			holders = append(holders, names[h.HolderPlayerID])
		}
		role := v.Acting.OfficeCode
		if v.Acting.Deputy {
			role += " (acting for the vacant " + l.HeldBy + ")"
		}
		fmt.Printf("    decided:  by %s: %s\n", role, strings.Join(holders, ", "))
	}
	fmt.Printf("    limits:   cooldown %s, notice %s\n", l.ChangeCooldown, l.Notice)
	return nil
}

// formatLever renders a scalar value in its type's unit. Operator output,
// not player text: integer arithmetic, no localisation.
func formatLever(typ string, v int64) string {
	if typ == "bps" {
		return fmt.Sprintf("%d bps (%d.%02d%%)", v, v/100, v%100)
	}
	return fmt.Sprint(v)
}
