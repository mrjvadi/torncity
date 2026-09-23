package application

import (
	"bufio"
	"context"
	stderrors "errors"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mrjvadi/torncity/internal/shared/errors"
	"github.com/mrjvadi/torncity/internal/shared/money"
)

// The ledger sentinels join the distinguishability checks in errors_test.go.
func init() {
	namedSentinels["ErrUnbalancedTransaction"] = ErrUnbalancedTransaction
	namedSentinels["ErrUnknownReason"] = ErrUnknownReason
	namedSentinels["ErrInvalidLedgerTransaction"] = ErrInvalidLedgerTransaction
	namedSentinels["ErrMixedCurrencies"] = ErrMixedCurrencies
	namedSentinels["ErrUnknownAccountKind"] = ErrUnknownAccountKind
	namedSentinels["ErrAccountNotFound"] = ErrAccountNotFound
	namedSentinels["ErrAccountOwnerNotFound"] = ErrAccountOwnerNotFound
	namedSentinels["ErrInsufficientFunds"] = ErrInsufficientFunds
	namedSentinels["ErrInvalidStartingCash"] = ErrInvalidStartingCash
}

func entry(account string, minor int64) LedgerEntry {
	return LedgerEntry{AccountID: account, Amount: money.FromMinor(minor)}
}

func TestValidateAcceptsABalancedTransaction(t *testing.T) {
	tx := LedgerTransaction{
		Reason:  ReasonStartingGrant,
		Entries: []LedgerEntry{entry(SystemSourceAccountID, -500), entry("p", 300), entry("q", 200)},
	}
	if err := tx.Validate(); err != nil {
		t.Fatalf("a balanced transaction was refused: %v", err)
	}
}

// TestUnbalancedTransactionIsRefused: money created or destroyed by one leg
// too many or too few never reaches the database.
func TestUnbalancedTransactionIsRefused(t *testing.T) {
	tests := []struct {
		name    string
		entries []LedgerEntry
	}{
		{"one side larger", []LedgerEntry{entry("a", -500), entry("b", 501)}},
		{"both sides credit", []LedgerEntry{entry("a", 1), entry("b", 1)}},
		{"a single leg", []LedgerEntry{entry("a", 100)}},
		{"no legs at all", nil},
		{"sum overflows int64", []LedgerEntry{entry("a", math.MaxInt64), entry("b", 1), entry("c", math.MinInt64)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := LedgerTransaction{Reason: ReasonAdminGrant, Entries: tc.entries}.Validate()
			if !stderrors.Is(err, ErrUnbalancedTransaction) {
				t.Fatalf("Validate() = %v, want ErrUnbalancedTransaction", err)
			}
		})
	}
}

func TestUnknownReasonIsRefused(t *testing.T) {
	for _, r := range []Reason{"", "gift", "Starting_Grant", "salary"} {
		err := LedgerTransaction{
			Reason:  r,
			Entries: []LedgerEntry{entry("a", -1), entry("b", 1)},
		}.Validate()
		if !stderrors.Is(err, ErrUnknownReason) {
			t.Errorf("reason %q: Validate() = %v, want ErrUnknownReason", r, err)
		}
	}
}

func TestMalformedTransactionIsRefused(t *testing.T) {
	tests := []struct {
		name string
		tx   LedgerTransaction
	}{
		{"a leg of zero", LedgerTransaction{Reason: ReasonTax,
			Entries: []LedgerEntry{entry("a", -1), entry("b", 1), entry("c", 0)}}},
		{"a leg with no account", LedgerTransaction{Reason: ReasonTax,
			Entries: []LedgerEntry{entry("a", -1), entry("", 1)}}},
		{"a reference type without an id", LedgerTransaction{Reason: ReasonTax, ReferenceType: "travels",
			Entries: []LedgerEntry{entry("a", -1), entry("b", 1)}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.tx.Validate(); !stderrors.Is(err, ErrInvalidLedgerTransaction) {
				t.Fatalf("Validate() = %v, want ErrInvalidLedgerTransaction", err)
			}
		})
	}
}

// TestBalancingIsExactNotFloat proves the balance check is integer
// arithmetic. 2^53+1 is the first integer a float64 cannot hold; converted to
// float64 it becomes 2^53, so a float-based check would see these two legs as
// balanced. The ledger must see the one unit of money that was created.
func TestBalancingIsExactNotFloat(t *testing.T) {
	const big = int64(1)<<53 + 1
	if float64(big) != float64(big-1) {
		t.Fatal("test premise broken: float64 now distinguishes 2^53 from 2^53+1")
	}

	err := LedgerTransaction{
		Reason:  ReasonAdminGrant,
		Entries: []LedgerEntry{entry("a", -(big - 1)), entry("b", big)},
	}.Validate()
	if !stderrors.Is(err, ErrUnbalancedTransaction) {
		t.Fatalf("a transaction off by one unit at 2^53 was accepted (%v): the check went through a float", err)
	}

	if err := (LedgerTransaction{
		Reason:  ReasonAdminGrant,
		Entries: []LedgerEntry{entry("a", -big), entry("b", big)},
	}).Validate(); err != nil {
		t.Fatalf("an exactly balanced transaction at 2^53+1 was refused: %v", err)
	}
}

// TestMoneyCodeHasNoFloats keeps float types out of every file that carries a
// ledger amount, so no future edit can route money through one.
func TestMoneyCodeHasNoFloats(t *testing.T) {
	files := []string{
		"ports_ledger.go",
		"starting_grant.go",
		filepath.Join("..", "infrastructure", "postgres", "ledger.go"),
		filepath.Join("..", "infrastructure", "postgres", "ledger_admin.go"),
		filepath.Join("..", "shared", "money", "money.go"),
	}
	banned := map[string]bool{"float32": true, "float64": true, "ParseFloat": true, "FormatFloat": true}

	for _, path := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && banned[id.Name] {
				t.Errorf("%s: %s used in money code", fset.Position(id.Pos()), id.Name)
			}
			return true
		})
	}
}

// TestReasonsMatchADR enforces ADR 0009 section 2: "no new reason is allowed
// without being added to this table". The Go set and the ADR's faucet and
// drain tables must list exactly the same codes.
func TestReasonsMatchADR(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "adr", "0009-economic-control.md")
	f, err := os.Open(path)
	if stderrors.Is(err, os.ErrNotExist) {
		// docs/ is listed in .gitignore, so a clean checkout (CI) has no ADR
		// to compare against. The check runs wherever the docs are present.
		t.Skipf("%s is not in this checkout; skipping the ADR cross-check", path)
	}
	if err != nil {
		t.Fatalf("opening the ADR: %v", err)
	}
	defer f.Close()

	// A table row whose second cell is a single backticked code.
	row := regexp.MustCompile("^\\|[^|]*\\|\\s*`([a-z_]+)`\\s*\\|")
	inSection := false
	var fromADR []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "## ") {
			// Section 2 is the only one whose heading names faucets and drains.
			inSection = strings.Contains(line, "۲")
			continue
		}
		if !inSection {
			continue
		}
		if m := row.FindStringSubmatch(line); m != nil && m[1] != "reason" {
			fromADR = append(fromADR, m[1])
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("reading the ADR: %v", err)
	}
	sort.Strings(fromADR)

	var fromCode []string
	for _, r := range Reasons() {
		fromCode = append(fromCode, string(r))
	}

	if strings.Join(fromADR, ",") != strings.Join(fromCode, ",") {
		t.Errorf("reason codes disagree with ADR 0009 section 2\n  ADR:  %v\n  code: %v", fromADR, fromCode)
	}
	for _, must := range []Reason{ReasonStartingGrant, ReasonTravelFare, ReasonAdminGrant} {
		if !must.Known() {
			t.Errorf("%s is missing from the closed set", must)
		}
	}
}

func TestAccountKinds(t *testing.T) {
	for _, k := range []AccountKind{AccountSystemSource, AccountSystemSink} {
		if !k.Valid() || !k.IsSystem() {
			t.Errorf("%s should be a valid system kind", k)
		}
	}
	if !AccountPlayerCash.Valid() || AccountPlayerCash.IsSystem() {
		t.Error("player_cash should be a valid, owned kind")
	}
	if AccountKind("wallet").Valid() {
		t.Error("an unknown kind was accepted")
	}
}

// fakeLedger records what GrantStartingCash asks of the ledger.
type fakeLedger struct {
	granted    map[string]bool
	posted     []LedgerTransaction
	accountErr error
}

func (f *fakeLedger) AccountFor(_ context.Context, kind AccountKind, owner string) (Account, error) {
	if f.accountErr != nil {
		return Account{}, f.accountErr
	}
	return Account{ID: "acct-" + owner, Kind: kind, OwnerID: owner, Currency: DefaultCurrency}, nil
}

func (f *fakeLedger) Balance(context.Context, string) (money.Amount, error) {
	return money.Amount{}, nil
}

func (f *fakeLedger) Post(_ context.Context, t LedgerTransaction) (string, error) {
	if err := t.Validate(); err != nil {
		return "", err
	}
	f.posted = append(f.posted, t)
	return t.ID, nil
}

func (f *fakeLedger) RecordGrant(_ context.Context, g RewardGrant) (RewardGrant, bool, error) {
	if g.Source == RewardStartingGrant && f.granted[g.PlayerID] {
		return RewardGrant{}, false, nil
	}
	f.granted[g.PlayerID] = true
	g.ID, g.LedgerTransactionID = "grant-1", "tx-1"
	return g, true, nil
}

func TestGrantStartingCashPaysOnce(t *testing.T) {
	ledger := &fakeLedger{granted: map[string]bool{}}
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	ok, err := GrantStartingCash(ctx, ledger, "p1", money.FromMinor(5000), "test", now)
	if err != nil || !ok {
		t.Fatalf("first grant = %v, %v; want true, nil", ok, err)
	}
	if len(ledger.posted) != 1 {
		t.Fatalf("posted %d transactions, want 1", len(ledger.posted))
	}
	tx := ledger.posted[0]
	if tx.Reason != ReasonStartingGrant || tx.ID != "tx-1" ||
		tx.ReferenceType != "reward_grants" || tx.ReferenceID != "grant-1" {
		t.Errorf("unexpected transaction header: %+v", tx)
	}
	want := []LedgerEntry{entry(SystemSourceAccountID, -5000), entry("acct-p1", 5000)}
	if len(tx.Entries) != 2 || tx.Entries[0] != want[0] || tx.Entries[1] != want[1] {
		t.Errorf("entries = %+v, want %+v", tx.Entries, want)
	}

	ok, err = GrantStartingCash(ctx, ledger, "p1", money.FromMinor(5000), "test", now)
	if err != nil || ok {
		t.Fatalf("second grant = %v, %v; want false, nil", ok, err)
	}
	if len(ledger.posted) != 1 {
		t.Fatalf("the second grant posted money: %d transactions", len(ledger.posted))
	}
}

func TestGrantStartingCashRefusesNothingOrLess(t *testing.T) {
	for _, minor := range []int64{0, -1} {
		ledger := &fakeLedger{granted: map[string]bool{}}
		_, err := GrantStartingCash(context.Background(), ledger, "p", money.FromMinor(minor), "t", time.Now())
		if !stderrors.Is(err, ErrInvalidStartingCash) {
			t.Errorf("amount %d: err = %v, want ErrInvalidStartingCash", minor, err)
		}
		if len(ledger.granted) != 0 {
			t.Errorf("amount %d: a grant was recorded", minor)
		}
	}
}

func TestGrantStartingCashUnknownPlayer(t *testing.T) {
	ledger := &fakeLedger{granted: map[string]bool{}, accountErr: ErrPlayerNotFound}
	_, err := GrantStartingCash(context.Background(), ledger, "ghost", money.FromMinor(1), "t", time.Now())
	if !stderrors.Is(err, ErrPlayerNotFound) {
		t.Fatalf("err = %v, want ErrPlayerNotFound", err)
	}
	if !stderrors.Is(err, errors.ErrNotFound) {
		t.Fatalf("err = %v no longer matches its class", err)
	}
}
