package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
)

// Operator tooling for a city's Telegram groups (migrations/0016). A city is
// played in its group: the group reads the city's public news, and a player
// is pointed there for what is done in a group. Until players found their own
// cities — founding will write the same link — an operator links an existing
// city to a group. Every link and unlink writes an audit row in the same
// transaction.

func cityUsage() {
	fmt.Fprint(os.Stderr, `usage: admin city <command>

  link-group   --city CODE --chat CHAT_ID --bot BOT_KEY --reason "why"
               [--language fa] [--by NAME]
                          link a Telegram group (its negative chat id) to a
                          city; the bot is the one that serves the group and
                          posts there; the language is the group's
  unlink-group --city CODE --chat CHAT_ID --reason "why" [--by NAME]
                          remove the link
  groups                  every city's groups
  show --city CODE        the city's overview (see: admin dashboard)

--by names the operator in the audit row; it defaults to $`+operatorEnv+`, then
$USER, and the change is refused when none of them names anybody.

DATABASE_URL must be set.
`)
}

// cityCommand dispatches the city subcommands.
func cityCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		cityUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "link-group":
		return cityGroupChange(ctx, args[1:], true)
	case "unlink-group":
		return cityGroupChange(ctx, args[1:], false)
	case "groups":
		return cityGroups(ctx)
	case "show":
		return cityShow(ctx, args[1:])
	}
	cityUsage()
	os.Exit(2)
	return nil
}

// languageCode is what a group's language may be: a locale file's name.
var languageCode = regexp.MustCompile(`^[a-z]{2,3}$`)

func cityGroupChange(ctx context.Context, args []string, link bool) error {
	name := "city unlink-group"
	if link {
		name = "city link-group"
	}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = cityUsage
	city := fs.String("city", "", "the city's content code, e.g. ostmarch")
	chat := fs.Int64("chat", 0, "the Telegram group's chat id (negative)")
	bot := fs.String("bot", "", "the bot that serves the group (telegram_bots.bot_key)")
	language := fs.String("language", "fa", "the group's language")
	reason := fs.String("reason", "", "why (required)")
	operator := addOperatorFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Checked before the database is dialled, like every audited command.
	switch {
	case strings.TrimSpace(*reason) == "":
		return fmt.Errorf("%s: --reason is required", name)
	case strings.TrimSpace(*city) == "":
		return fmt.Errorf("%s: --city is required", name)
	case *chat >= 0:
		return fmt.Errorf("%s: --chat must be a group's chat id, which is negative", name)
	case link && strings.TrimSpace(*bot) == "":
		return fmt.Errorf("%s: --bot is required", name)
	case link && !languageCode.MatchString(*language):
		return fmt.Errorf("%s: --language %q is not a language code", name, *language)
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
	repo := postgres.NewCityGroupRepository(pool)
	ch := postgres.CityGroupChange{
		CityCode: strings.TrimSpace(*city), ChatID: *chat, BotKey: strings.TrimSpace(*bot),
		Language: *language, Actor: who, Reason: strings.TrimSpace(*reason), At: time.Now().UTC(),
	}
	if !link {
		if err := repo.Unlink(ctx, ch); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		fmt.Printf("unlinked group %d from %s\nby:     %s\nreason: %s\n", ch.ChatID, ch.CityCode, who, ch.Reason)
		return nil
	}
	g, err := repo.Link(ctx, ch)
	if errors.Is(err, postgres.ErrCityGroupTaken) {
		return fmt.Errorf("%s: group %d already plays another city; unlink it there first", name, ch.ChatID)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	fmt.Printf("linked group %d to %s through %s, in %s\nby:     %s\nreason: %s\n",
		g.ChatID, ch.CityCode, ch.BotKey, g.Language, who, ch.Reason)
	return nil
}

func cityGroups(ctx context.Context) error {
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	groups, err := postgres.NewCityGroupRepository(pool).All(ctx)
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		fmt.Println("no city is linked to a group")
		return nil
	}
	for _, g := range groups {
		fmt.Printf("  %-16s %16d  %s  linked by %s at %s\n", g.CityCode, g.ChatID, g.Language, g.LinkedBy,
			g.LinkedAt.Format(time.RFC3339))
	}
	return nil
}
