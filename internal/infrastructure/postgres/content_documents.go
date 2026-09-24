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
