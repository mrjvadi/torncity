package notification

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Which private notice (a Route with Render, never Announce — a group line
// always posts at once, untouched) sends at once and which joins the inbox
// badge is an operator's decision, not code: configs/notifications/
// delivery.yml. An operator changes a kind's mode, or adds one, and restarts
// cmd/notifier; nothing here is redeployed for that.
//
// A DeliveryModes built from the file is nil-safe as a zero value used by
// nothing: cmd/notifier always loads the shipped file and fails to start if
// it cannot (LoadDeliveryModes), the same as content.Load. A Worker built
// with no DeliveryModes at all (every existing unit test, and any caller
// that has not wired PlayerInbox) sends every kind at once, exactly as
// before this feature existed — see Worker.classify.

// Mode is a notification kind's delivery mode.
type Mode string

const (
	// ModeInstant sends at once, its own Telegram message, as every notice
	// did before this feature.
	ModeInstant Mode = "instant"
	// ModeInbox stores the notice and counts it on the player's inbox
	// badge instead of sending it.
	ModeInbox Mode = "inbox"
)

// Valid reports whether m is a mode the file may declare.
func (m Mode) Valid() bool { return m == ModeInstant || m == ModeInbox }

// kindPolicy is one kind's classification.
type kindPolicy struct {
	Mode     Mode
	Category string
}

// DeliveryModes classifies every "<domain>.<event>" kind a private notice
// may produce.
type DeliveryModes struct {
	defaultMode Mode
	kinds       map[string]kindPolicy
	// categories is the fallback category by domain, for a kind the file
	// does not name explicitly; a domain absent from both falls back to
	// its own name.
	categories map[string]string
}

// deliveryFile mirrors configs/notifications/delivery.yml.
type deliveryFile struct {
	DefaultMode string            `yaml:"default_mode"`
	Categories  map[string]string `yaml:"categories"`
	Kinds       []struct {
		Domain   string `yaml:"domain"`
		Event    string `yaml:"event"`
		Mode     string `yaml:"mode"`
		Category string `yaml:"category"`
	} `yaml:"kinds"`
}

// LoadDeliveryModes reads and validates the file. A broken file is a fatal
// error at startup (content.Load's own rule): a typo silently defaulting
// every kind to "inbox" would bury reports players are supposed to see at
// once, and nobody would notice until someone complained about never being
// told they were attacked.
func LoadDeliveryModes(path string) (*DeliveryModes, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("notification: reading %s: %w", path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var f deliveryFile
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("notification: %s: %w", path, err)
	}

	d := &DeliveryModes{
		defaultMode: Mode(f.DefaultMode),
		kinds:       map[string]kindPolicy{},
		categories:  map[string]string{},
	}
	if d.defaultMode == "" {
		d.defaultMode = ModeInbox
	}
	if !d.defaultMode.Valid() {
		return nil, fmt.Errorf("notification: %s: default_mode %q is neither instant nor inbox", path, f.DefaultMode)
	}
	for domain, category := range f.Categories {
		if domain == "" || category == "" {
			return nil, fmt.Errorf("notification: %s: a category mapping has an empty domain or category", path)
		}
		d.categories[domain] = category
	}
	for _, k := range f.Kinds {
		if k.Domain == "" || k.Event == "" {
			return nil, fmt.Errorf("notification: %s: a kind is missing its domain or event", path)
		}
		mode := Mode(k.Mode)
		if !mode.Valid() {
			return nil, fmt.Errorf("notification: %s: %s.%s has mode %q, neither instant nor inbox", path, k.Domain, k.Event, k.Mode)
		}
		category := k.Category
		if category == "" {
			category = d.categoryOf(k.Domain)
		}
		key := k.Domain + "." + k.Event
		if _, dup := d.kinds[key]; dup {
			return nil, fmt.Errorf("notification: %s: %s is listed twice", path, key)
		}
		d.kinds[key] = kindPolicy{Mode: mode, Category: category}
	}
	return d, nil
}

// categoryOf is the fallback category for a domain the kind table's own
// entry (if any) did not name one for.
func (d *DeliveryModes) categoryOf(domain string) string {
	if c, ok := d.categories[domain]; ok {
		return c
	}
	return domain
}

// Classify returns kind's delivery mode and inbox category.
func (d *DeliveryModes) Classify(domain, event string) (Mode, string) {
	key := domain + "." + event
	if p, ok := d.kinds[key]; ok {
		return p.Mode, p.Category
	}
	return d.defaultMode, d.categoryOf(domain)
}
