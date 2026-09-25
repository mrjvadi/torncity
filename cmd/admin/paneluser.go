package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/panel/credential"
)

// The web panel's accounts (cmd/panel, migrations/0030) are made and changed
// here, on the server, and nowhere else: the panel itself cannot create an
// account or change a password. A password is never a flag — it would land
// in the shell's history — but is typed at a prompt that does not echo, or
// read from one line of stdin when stdin is not a terminal.

func panelUserUsage() {
	fmt.Fprint(os.Stderr, `usage: admin panel user <command>

  add     --username NAME --reason "why" [--by NAME]
                     create an operator account; the password is asked for
  passwd  --username NAME --reason "why" [--by NAME]
                     set a new password; every session of the account ends
  disable --username NAME --reason "why" [--by NAME]
                     refuse the account's sign-in; its sessions end
  enable  --username NAME --reason "why" [--by NAME]
  totp    --username NAME --reason "why" [--off] [--by NAME]
                     enrol the account in two-factor sign-in: prints the
                     secret and its otpauth:// URI once, then asks for a
                     code from the app to confirm; --off removes it
  list               every account, its state and last sign-in

Every change writes an audit row. DATABASE_URL must be set; TORN_CONFIG gives
panel.password_min_length and panel.totp_issuer.
`)
}

// panelUsername is what an account may be called (panel_accounts_username_check).
var panelUsername = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

// panelCommand dispatches `admin panel user ...`.
func panelCommand(ctx context.Context, args []string) error {
	if len(args) < 2 || args[0] != "user" {
		panelUserUsage()
		os.Exit(2)
	}
	sub, rest := args[1], args[2:]
	if sub == "list" {
		return panelUserList(ctx)
	}
	switch sub {
	case "add", "passwd", "disable", "enable", "totp":
	default:
		panelUserUsage()
		os.Exit(2)
	}
	name := "panel user " + sub
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = panelUserUsage
	username := fs.String("username", "", "the account's username (required)")
	reason := fs.String("reason", "", "why (required)")
	off := fs.Bool("off", false, "totp: remove two-factor sign-in instead")
	operator := addOperatorFlags(fs)
	if err := fs.Parse(rest); err != nil {
		return err
	}
	user := strings.ToLower(strings.TrimSpace(*username))
	switch {
	case !panelUsername.MatchString(user):
		return fmt.Errorf("%s: --username must be 3-32 of a-z 0-9 . _ - starting with a letter or digit", name)
	case strings.TrimSpace(*reason) == "":
		return fmt.Errorf("%s: --reason is required", name)
	}
	who, err := operator.resolve(name, os.LookupEnv)
	if err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	ch := postgres.PanelAccountChange{Username: user, Actor: who, Reason: strings.TrimSpace(*reason), At: time.Now().UTC()}

	// The password is read before the database is dialled.
	var hash string
	if sub == "add" || sub == "passwd" {
		pw, err := readNewPassword(cfg.Panel.PasswordMinLength)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if hash, err = credential.HashPassword(pw); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}

	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	accounts := postgres.NewPanelAccounts(pool)
	switch sub {
	case "add":
		err = accounts.Create(ctx, ch, hash)
	case "passwd":
		err = accounts.SetPassword(ctx, ch, hash)
	case "disable", "enable":
		err = accounts.SetStatus(ctx, ch, sub == "enable")
	case "totp":
		err = panelTOTP(ctx, accounts, ch, cfg.Panel.TOTPIssuer, *off)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	fmt.Printf("%s: done for %s\nby:     %s\nreason: %s\n", name, user, who, ch.Reason)
	return nil
}

// panelTOTP enrols (or, with off, removes) two-factor sign-in.
func panelTOTP(ctx context.Context, accounts *postgres.PanelAccounts, ch postgres.PanelAccountChange, issuer string, off bool) error {
	if off {
		return accounts.SetTOTP(ctx, ch, "")
	}
	if _, err := accounts.ByUsername(ctx, ch.Username); err != nil {
		return err
	}
	secret, err := credential.NewTOTPSecret()
	if err != nil {
		return err
	}
	fmt.Println("add this to an authenticator app (it is shown only now):")
	fmt.Printf("  secret: %s\n  uri:    %s\n", secret, credential.TOTPURI(issuer, ch.Username, secret))
	code, err := readLine("code from the app to confirm: ", false)
	if err != nil {
		return err
	}
	if !credential.VerifyTOTP(secret, code, time.Now()) {
		return errors.New("that code does not match; nothing was changed, run it again")
	}
	return accounts.SetTOTP(ctx, ch, secret)
}

// readNewPassword asks for a password twice at a terminal, or reads one line
// of stdin otherwise, and checks its length.
func readNewPassword(minLength int) (string, error) {
	pw, err := readLine("new password: ", true)
	if err != nil {
		return "", err
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		again, err := readLine("again: ", true)
		if err != nil {
			return "", err
		}
		if again != pw {
			return "", errors.New("the two passwords differ")
		}
	}
	switch n := len([]rune(pw)); {
	case n < minLength:
		return "", fmt.Errorf("the password is shorter than %d characters", minLength)
	case len(pw) > 256:
		return "", errors.New("the password is longer than 256 bytes")
	}
	return pw, nil
}

var stdinLines = bufio.NewReader(os.Stdin)

// readLine prompts on stderr and reads one line, without echo when secret
// and stdin is a terminal.
func readLine(prompt string, secret bool) (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, prompt)
		if secret {
			b, err := term.ReadPassword(fd)
			fmt.Fprintln(os.Stderr)
			return string(b), err
		}
	}
	line, err := stdinLines.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", errors.New("nothing to read on stdin")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// panelUserList prints every account.
func panelUserList(ctx context.Context) error {
	pool, err := contentPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	list, err := postgres.NewPanelAccounts(pool).List(ctx)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("no panel accounts; add one with: admin panel user add --username NAME --reason \"...\"")
		return nil
	}
	for _, a := range list {
		last := "never"
		if a.LastLoginAt != nil {
			last = a.LastLoginAt.UTC().Format(time.RFC3339)
		}
		state := a.Status
		if a.LockedUntil != nil && a.LockedUntil.After(time.Now()) {
			state += ", locked until " + a.LockedUntil.UTC().Format(time.RFC3339)
		}
		fmt.Printf("  %-20s %-30s two-factor %-5v last sign-in %s  (created by %s)\n", a.Username, state, a.TOTPEnabled, last, a.CreatedBy)
	}
	return nil
}
