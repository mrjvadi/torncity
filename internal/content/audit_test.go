package content

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// defaultAudit are the numbers of configs/config.yml (settlement.founding_grant, export_cap_base and per resident for ten
// residents, travel.world_reach) the audit is made against.
func defaultAudit(t *testing.T) AuditOptions {
	t.Helper()
	o := AuditOptions{FoundingGrant: 10_000, EarnPerDay: 400 + 40*10, HorizonDays: 30, WalkKm: 40, CartKm: 150}
	root := filepath.Join("..", "..")
	lits, fields, err := ScanSource(filepath.Join(root, "internal"), filepath.Join(root, "cmd"))
	if err != nil {
		t.Logf("the Go source was not read, the reader check is skipped: %v", err)
		return o
	}
	o.Literals, o.Fields = lits, fields
	return o
}

// TestContentAuditReport prints today's violations of the content audit. It never fails on a finding: the list is the
// to-do of the plan (docs/adr/0056); AUDIT_OUT=path writes it to a file.
func TestContentAuditReport(t *testing.T) {
	p := shippedPack(t)
	if _, err := BuildSnapshot(1, p); err != nil {
		t.Fatal(err)
	}
	findings := p.Audit(defaultAudit(t))
	counts := AuditCounts(findings)
	t.Logf("content audit: %d findings (money %d, distance %d, reader %d, real %d)", len(findings), counts[AuditMoney], counts[AuditDistance], counts[AuditReader], counts[AuditReal])
	if out := os.Getenv("AUDIT_OUT"); out != "" {
		var text string
		for _, f := range findings {
			text += fmt.Sprintf("%s\t%s\t%s\t%s\n", f.Check, f.Kind, f.Code, f.Detail)
		}
		if err := os.WriteFile(out, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The audit finds what it claims to: a costly building, a planned item in use, an effect nobody reads.
func TestContentAuditFindsWhatItClaims(t *testing.T) {
	p := shippedPack(t)
	p.SettlementBuildings[0].CostMoney = 1_000_000_000
	p.SettlementBuildings[0].Effects = append(p.SettlementBuildings[0].Effects, EffectDef{Target: "nothing_reads_this_xyz", Op: "add", Value: 1})
	o := defaultAudit(t)
	if o.Literals == nil {
		t.Skip("no Go source to read")
	}
	var money, reader bool
	for _, f := range p.Audit(o) {
		money = money || (f.Check == AuditMoney && f.Code == p.SettlementBuildings[0].Code)
		reader = reader || (f.Check == AuditReader && f.Detail == `the effect target "nothing_reads_this_xyz" is read by no code`)
	}
	if !money || !reader {
		t.Errorf("money finding %v, reader finding %v", money, reader)
	}
}
