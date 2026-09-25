package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

func defenceUsage() {
	fmt.Fprint(os.Stderr, `usage: admin defence <command>

  grant --company CODE --reason "why" [--by NAME]
                     give an active company a defence contractor licence on
                     the operator's authority; its owner may then found
                     defence companies. Audited.

DATABASE_URL must be set.
`)
}

// defenceCommand dispatches the defence subcommands.
func defenceCommand(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "grant" {
		defenceUsage()
		os.Exit(2)
	}
	fs := flag.NewFlagSet("defence grant", flag.ExitOnError)
	fs.Usage = defenceUsage
	company := fs.String("company", "", "the company's public code (required)")
	reason := fs.String("reason", "", "why (required)")
	operator := addOperatorFlags(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	switch {
	case strings.TrimSpace(*company) == "":
		return errors.New("defence grant: --company is required")
	case strings.TrimSpace(*reason) == "":
		return errors.New("defence grant: --reason is required")
	}
	who, err := operator.resolve("defence grant", os.LookupEnv)
	if err != nil {
		return err
	}
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	no, name, err := postgres.GrantDefenceLicence(ctx, pool, postgres.OperatorLicence{
		CompanyCode: strings.TrimSpace(*company), Actor: who, Reason: strings.TrimSpace(*reason), At: time.Now()})
	if err != nil {
		return err
	}
	fmt.Printf("defence contractor licence #%d granted to %s\nby:     %s\nreason: %s\n", no, name, who, strings.TrimSpace(*reason))
	return nil
}
