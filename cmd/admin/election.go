package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/application/handlers"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
)

// Operator tooling for elections (internal/domain/election). An operator
// opens an office's first election; after its count, an office with a term
// opens its next one on its own when the term runs out.

func electionUsage() {
	fmt.Fprint(os.Stderr, `usage: admin election open --office <code> (--city <code> | --country <code>) --reason <text>

  open  open an election of an elected office in one place: candidacy, then
        the vote, for the real-time periods governance.yml gives it; the vote
        opening and the count are scheduled. TORN_CONFIG (default
        `+config.DefaultPath+`) gives the announcement's default language.

Every election opened writes an audit row with --by (or TORN_OPERATOR) and
--reason.
`)
}

// electionCommand dispatches the election subcommands.
func electionCommand(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "open" {
		electionUsage()
		os.Exit(2)
	}
	return electionOpen(ctx, args[1:])
}

// electionIDs draws new row ids.
type electionIDs struct{}

func (electionIDs) NewID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("admin: no randomness available for identifiers: " + err.Error())
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func electionOpen(ctx context.Context, args []string) error {
	const name = "election open"
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = electionUsage
	office := fs.String("office", "", "office code, e.g. mayor")
	place := addPlaceFlags(fs)
	reason := fs.String("reason", "", "why (required)")
	operator := addOperatorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*reason) == "" {
		return fmt.Errorf("%s: --reason is required", name)
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
	who, err := operator.resolve(name, os.LookupEnv)
	if err != nil {
		return err
	}
	cfgPath := os.Getenv("TORN_CONFIG")
	if cfgPath == "" {
		cfgPath = config.DefaultPath
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	pack, err := postgres.NewContentStore(pool).LoadActive(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	snap, err := content.BuildSnapshot(pack.Version, pack)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	j, err := postgres.NewGovernanceAdmin(pool).JurisdictionByCode(ctx, kind, code)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	now := time.Now().UTC()
	meta := envelope.Metadata{
		RequestID: electionIDs{}.NewID(), TraceID: electionIDs{}.NewID(), Command: "election.open",
		Language: cfg.Player.DefaultLanguage, SchemaVersion: envelope.SchemaVersion,
	}
	var opened application.Election
	uow := postgres.NewUnitOfWork(pool, cfg.Player.DefaultLanguage)
	if err := uow.Do(ctx, func(ctx context.Context, tx application.Tx) error {
		e, err := handlers.OpenElection(ctx, tx, electionIDs{}, snap,
			strings.TrimSpace(*office), j.ID, now)
		if err != nil {
			return err
		}
		opened = e
		return handlers.AnnounceElectionOpened(ctx, tx, postgres.NewCityRepository(pool), meta, e)
	}); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if err := postgres.NewEconomyAdmin(pool).AppendAudit(ctx, postgres.AuditEntry{
		Actor: who, Action: "election.open", TargetType: "election",
		NewValue: map[string]any{"election_id": opened.ID, "no": opened.No, "office": opened.OfficeCode,
			"jurisdiction": kind + ":" + code, "voting_ends_at": opened.VotingEndsAt},
		Reason: strings.TrimSpace(*reason), At: now,
	}); err != nil {
		return err
	}
	fmt.Printf("opened election %d: %s of %s %s\n", opened.No, opened.OfficeCode, kind, code)
	fmt.Printf("candidacy until: %s\n", opened.CandidacyEndsAt.Format(time.RFC3339))
	fmt.Printf("voting until:    %s (the count is scheduled then)\n", opened.VotingEndsAt.Format(time.RFC3339))
	fmt.Printf("by:              %s\n", who)
	return nil
}
