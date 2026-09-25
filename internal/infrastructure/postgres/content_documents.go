package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"hash"

	"github.com/jackc/pgx/v5"

	"github.com/mrjvadi/torncity/internal/content"
)

// This file writes and reads the content stored as authored documents in
// content_documents (migrations/0014_payments.up.sql): one row per entry, per
// content version, each entry the document it was parsed into, with its
// position so an ordered list reads back in its order. A new content type
// adds a kind to documentKinds below and nothing else here.

// documentKind is one list of the pack stored in content_documents.
type documentKind struct {
	kind string
	// entries returns the list's entries with their codes, in order.
	entries func(p *content.Pack) []documentEntry
	// add decodes one stored entry and appends it to the pack.
	add func(p *content.Pack, raw []byte) error
}

// documentEntry is one entry of a list, ready to store.
type documentEntry struct {
	code string
	doc  any
}

// documentKinds is every list stored as documents.
var documentKinds = []documentKind{
	{
		kind: "place",
		entries: func(p *content.Pack) []documentEntry {
			out := make([]documentEntry, 0, len(p.Venues))
			for _, v := range p.Venues {
				out = append(out, documentEntry{code: v.Code, doc: v})
			}
			return out
		},
		add: func(p *content.Pack, raw []byte) error {
			var v content.PlaceDef
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			p.Venues = append(p.Venues, v)
			return nil
		},
	},
	{
		kind: "payment_service",
		entries: func(p *content.Pack) []documentEntry {
			out := make([]documentEntry, 0, len(p.PaymentServices))
			for _, s := range p.PaymentServices {
				out = append(out, documentEntry{code: s.Code, doc: s})
			}
			return out
		},
		add: func(p *content.Pack, raw []byte) error {
			var s content.PaymentServiceDef
			if err := json.Unmarshal(raw, &s); err != nil {
				return err
			}
			p.PaymentServices = append(p.PaymentServices, s)
			return nil
		},
	},
}

// listKind stores a list of the pack whose entries have a code, as documents.
func listKind[T any](kind string, get func(p *content.Pack) []T, code func(T) string, put func(p *content.Pack, v T)) documentKind {
	return documentKind{
		kind: kind,
		entries: func(p *content.Pack) []documentEntry {
			list := get(p)
			out := make([]documentEntry, 0, len(list))
			for _, v := range list {
				out = append(out, documentEntry{code: code(v), doc: v})
			}
			return out
		},
		add: func(p *content.Pack, raw []byte) error {
			var v T
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			put(p, v)
			return nil
		},
	}
}

func init() {
	documentKinds = append(documentKinds,
		listKind("component_category",
			func(p *content.Pack) []string { return p.ComponentCategories },
			func(c string) string { return c },
			func(p *content.Pack, c string) { p.ComponentCategories = append(p.ComponentCategories, c) }),
		listKind("component",
			func(p *content.Pack) []content.ComponentDef { return p.Components },
			func(c content.ComponentDef) string { return c.Code },
			func(p *content.Pack, c content.ComponentDef) { p.Components = append(p.Components, c) }),
		listKind("archetype",
			func(p *content.Pack) []content.ArchetypeDef { return p.Archetypes },
			func(a content.ArchetypeDef) string { return a.Code },
			func(p *content.Pack, a content.ArchetypeDef) { p.Archetypes = append(p.Archetypes, a) }),
		listKind("item",
			func(p *content.Pack) []content.ItemDef { return p.Items },
			func(i content.ItemDef) string { return i.Code },
			func(p *content.Pack, i content.ItemDef) { p.Items = append(p.Items, i) }),
		listKind("shop",
			func(p *content.Pack) []content.ShopDef { return p.Shops },
			func(s content.ShopDef) string { return s.Code },
			func(p *content.Pack, s content.ShopDef) { p.Shops = append(p.Shops, s) }),
		listKind("election",
			func(p *content.Pack) []content.ElectionDef { return p.Elections },
			func(e content.ElectionDef) string { return e.Office },
			func(p *content.Pack, e content.ElectionDef) { p.Elections = append(p.Elections, e) }),
		listKind("company_type",
			func(p *content.Pack) []content.CompanyTypeDef { return p.CompanyTypes },
			func(t content.CompanyTypeDef) string { return t.Code },
			func(p *content.Pack, t content.CompanyTypeDef) { p.CompanyTypes = append(p.CompanyTypes, t) }),
		listKind("company_market",
			func(p *content.Pack) []content.CompanyMarketDef { return p.CompanyMarkets },
			func(m content.CompanyMarketDef) string { return m.City },
			func(p *content.Pack, m content.CompanyMarketDef) { p.CompanyMarkets = append(p.CompanyMarkets, m) }),
		listKind("company_demand",
			func(p *content.Pack) []content.CompanyDemandDef { return p.CompanyDemand },
			func(d content.CompanyDemandDef) string { return d.Category },
			func(p *content.Pack, d content.CompanyDemandDef) { p.CompanyDemand = append(p.CompanyDemand, d) }),
		listKind("production_method",
			func(p *content.Pack) []content.MethodProfileDef { return p.MethodProfiles },
			func(d content.MethodProfileDef) string { return d.Method },
			func(p *content.Pack, d content.MethodProfileDef) { p.MethodProfiles = append(p.MethodProfiles, d) }),
		listKind("technology",
			func(p *content.Pack) []content.TechnologyDef { return p.Technologies },
			func(t content.TechnologyDef) string { return t.Code },
			func(p *content.Pack, t content.TechnologyDef) { p.Technologies = append(p.Technologies, t) }),
		listKind("supplier",
			func(p *content.Pack) []content.SupplierDef { return p.Suppliers },
			func(s content.SupplierDef) string { return s.Code },
			func(p *content.Pack, s content.SupplierDef) { p.Suppliers = append(p.Suppliers, s) }),
		listKind("action",
			func(p *content.Pack) []content.ActionDef { return p.Actions },
			func(a content.ActionDef) string { return a.Code },
			func(p *content.Pack, a content.ActionDef) { p.Actions = append(p.Actions, a) }),
		listKind("force_branch",
			func(p *content.Pack) []content.BranchDef { return p.Branches },
			func(b content.BranchDef) string { return b.Code },
			func(p *content.Pack, b content.BranchDef) { p.Branches = append(p.Branches, b) }),
		listKind("force_class",
			func(p *content.Pack) []content.ForceClassDef { return p.ForceClasses },
			func(c content.ForceClassDef) string { return c.Code },
			func(p *content.Pack, c content.ForceClassDef) { p.ForceClasses = append(p.ForceClasses, c) }),
		listKind("military_clearance",
			func(p *content.Pack) []string { return p.MilitaryClearance },
			func(o string) string { return o },
			func(p *content.Pack, o string) { p.MilitaryClearance = append(p.MilitaryClearance, o) }),
		listKind("strength_band",
			func(p *content.Pack) []content.StrengthBandDef { return p.StrengthBands },
			func(b content.StrengthBandDef) string { return b.Code },
			func(p *content.Pack, b content.StrengthBandDef) { p.StrengthBands = append(p.StrengthBands, b) }),
		listKind("treaty_type",
			func(p *content.Pack) []content.TreatyTypeDef { return p.TreatyTypes },
			func(t content.TreatyTypeDef) string { return t.Code },
			func(p *content.Pack, t content.TreatyTypeDef) { p.TreatyTypes = append(p.TreatyTypes, t) }),
		listKind("war",
			func(p *content.Pack) []content.WarDef { return p.War },
			func(content.WarDef) string { return "war" },
			func(p *content.Pack, w content.WarDef) { p.War = append(p.War, w) }),
		listKind("health",
			func(p *content.Pack) []content.HealthDef { return p.Health },
			func(content.HealthDef) string { return "health" },
			func(p *content.Pack, h content.HealthDef) { p.Health = append(p.Health, h) }),
		listKind("mission_board",
			func(p *content.Pack) []content.MissionBoardDef { return p.MissionBoards },
			func(b content.MissionBoardDef) string { return b.Code },
			func(p *content.Pack, b content.MissionBoardDef) { p.MissionBoards = append(p.MissionBoards, b) }),
		listKind("mission",
			func(p *content.Pack) []content.MissionDef { return p.Missions },
			func(m content.MissionDef) string { return m.Code },
			func(p *content.Pack, m content.MissionDef) { p.Missions = append(p.Missions, m) }),
		listKind("faction",
			func(p *content.Pack) []content.FactionDef { return p.Factions },
			func(content.FactionDef) string { return "faction" },
			func(p *content.Pack, f content.FactionDef) { p.Factions = append(p.Factions, f) }),
		listKind("sanction_ground",
			func(p *content.Pack) []string { return p.SanctionGrounds },
			func(g string) string { return g },
			func(p *content.Pack, g string) { p.SanctionGrounds = append(p.SanctionGrounds, g) }),
		listKind("budget",
			func(p *content.Pack) []content.BudgetDef { return p.Budget },
			func(b content.BudgetDef) string { return b.Lever },
			func(p *content.Pack, b content.BudgetDef) { p.Budget = append(p.Budget, b) }),
		listKind("property",
			func(p *content.Pack) []content.PropertyDef { return p.Property },
			func(content.PropertyDef) string { return "property" },
			func(p *content.Pack, d content.PropertyDef) { p.Property = append(p.Property, d) }),
		listKind("property_type",
			func(p *content.Pack) []content.PropertyTypeDef { return p.PropertyTypes },
			func(t content.PropertyTypeDef) string { return t.Code },
			func(p *content.Pack, t content.PropertyTypeDef) { p.PropertyTypes = append(p.PropertyTypes, t) }),
		listKind("property_market",
			func(p *content.Pack) []content.PropertyMarketDef { return p.PropertyMarkets },
			func(m content.PropertyMarketDef) string { return m.City },
			func(p *content.Pack, m content.PropertyMarketDef) { p.PropertyMarkets = append(p.PropertyMarkets, m) }),
		listKind("achievement",
			func(p *content.Pack) []content.AchievementDef { return p.Achievements },
			func(a content.AchievementDef) string { return a.Code },
			func(p *content.Pack, a content.AchievementDef) { p.Achievements = append(p.Achievements, a) }),
		listKind("company_reserved_name",
			func(p *content.Pack) []string { return p.CompanyReservedNames },
			func(w string) string { return w },
			func(p *content.Pack, w string) { p.CompanyReservedNames = append(p.CompanyReservedNames, w) }),
	)
}

// insertDocuments writes every document list of the pack under versionID.
func insertDocuments(ctx context.Context, tx pgx.Tx, p *content.Pack, versionID string) error {
	for _, k := range documentKinds {
		for i, e := range k.entries(p) {
			doc, err := json.Marshal(e.doc)
			if err != nil {
				return fmt.Errorf("postgres: content apply: %s %q: %w", k.kind, e.code, err)
			}
			id, err := newUUID()
			if err != nil {
				return fmt.Errorf("postgres: content apply: %s %q: %w", k.kind, e.code, err)
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO content_documents (id, content_version_id, kind, code, position, definition)
				 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6::jsonb)`,
				id, versionID, k.kind, e.code, i, string(doc)); err != nil {
				return fmt.Errorf("postgres: content apply: %s %q: %w", k.kind, e.code, err)
			}
		}
	}
	return nil
}

// loadDocuments reads one version's document lists into pack, each in its
// authored order.
func loadDocuments(ctx context.Context, tx pgx.Tx, versionID string, pack *content.Pack) error {
	byKind := make(map[string]documentKind, len(documentKinds))
	for _, k := range documentKinds {
		byKind[k.kind] = k
	}
	rows, err := tx.Query(ctx,
		`SELECT kind, code, definition FROM content_documents
		  WHERE content_version_id = $1::uuid
		  ORDER BY kind, position, code`, versionID)
	if err != nil {
		return fmt.Errorf("postgres: content load: documents: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			kind, code string
			raw        []byte
		)
		if err := rows.Scan(&kind, &code, &raw); err != nil {
			return fmt.Errorf("postgres: content load: scanning a document: %w", err)
		}
		k, ok := byKind[kind]
		if !ok {
			return fmt.Errorf("postgres: content load: unknown document kind %q (%s)", kind, code)
		}
		if err := k.add(pack, raw); err != nil {
			return fmt.Errorf("postgres: content load: decoding %s %q: %w", kind, code, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: content load: reading documents: %w", err)
	}
	return nil
}

// checksumDocuments adds the document lists to a value checksum, in the
// stored form, so a pack from files and the same pack read back fingerprint
// alike.
func checksumDocuments(h hash.Hash, p *content.Pack) {
	for _, k := range documentKinds {
		for _, e := range k.entries(p) {
			doc, _ := json.Marshal(e.doc)
			fmt.Fprintf(h, "%s|%s\n", k.kind, doc)
		}
	}
}
