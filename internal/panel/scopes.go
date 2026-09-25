package panel

import (
	"regexp"
	"strings"
)

// A scope narrows a list to one thing's rows: a player's, a company's, a
// city's. The request names the thing the way an operator does — a player
// by public code, a war by its number — and the scope resolves it to the
// id the view's predicate compares, once, before the list is read.

// scopeKind is how one scope's value is read and resolved.
type scopeKind struct {
	// resolve turns the normalised value into the id the predicates
	// compare; empty takes the value as it is.
	resolve string
	// norm normalises the value before it is checked and resolved.
	norm func(string) string
	// valid says whether the value may be looked up at all.
	valid *regexp.Regexp
}

var (
	upperCode  = regexp.MustCompile(`^[A-Z0-9]{3,16}$`)
	lowerCode  = regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)
	wholeNo    = regexp.MustCompile(`^[0-9]{1,18}$`)
	uuidText   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	itemCode   = regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)
	serialCode = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
)

func upper(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
func trimNo(s string) string {
	return strings.TrimPrefix(strings.TrimSpace(s), "#")
}

// ScopeNames are the scopes a request may carry, as query parameters.
var scopeKinds = map[string]scopeKind{
	"player":    {resolve: `SELECT id::text FROM players WHERE public_code = $1`, norm: upper, valid: upperCode},
	"company":   {resolve: `SELECT id::text FROM companies WHERE code = $1`, norm: upper, valid: upperCode},
	"city":      {resolve: `SELECT id::text FROM cities WHERE code = $1`, norm: lower, valid: lowerCode},
	"here":      {resolve: `SELECT id::text FROM cities WHERE code = $1`, norm: lower, valid: lowerCode},
	"country":   {resolve: `SELECT id::text FROM jurisdictions WHERE kind = 'country' AND code = $1`, norm: lower, valid: lowerCode},
	"faction":   {resolve: `SELECT id::text FROM factions WHERE code = $1`, norm: upper, valid: upperCode},
	"war":       {resolve: `SELECT id::text FROM wars WHERE no = $1::bigint`, norm: trimNo, valid: wholeNo},
	"election":  {resolve: `SELECT id::text FROM elections WHERE no = $1::bigint`, norm: trimNo, valid: wholeNo},
	"proposal":  {resolve: `SELECT id::text FROM proposals WHERE no = $1::bigint`, norm: trimNo, valid: wholeNo},
	"loan":      {resolve: `SELECT id::text FROM loans WHERE no = $1::bigint`, norm: trimNo, valid: wholeNo},
	"policy":    {resolve: `SELECT id::text FROM insurance_policies WHERE no = $1::bigint`, norm: trimNo, valid: wholeNo},
	"auction":   {resolve: `SELECT id::text FROM auctions WHERE no = $1::bigint`, norm: trimNo, valid: wholeNo},
	"operation": {resolve: `SELECT id::text FROM war_operations WHERE no = $1::bigint`, norm: trimNo, valid: wholeNo},
	"crew":      {resolve: `SELECT id::text FROM faction_operations WHERE no = $1::bigint`, norm: trimNo, valid: wholeNo},
	"property":  {resolve: `SELECT id::text FROM properties WHERE no = $1::bigint`, norm: trimNo, valid: wholeNo},
	"design":    {resolve: `SELECT id::text FROM product_designs WHERE no = $1::bigint`, norm: trimNo, valid: wholeNo},
	"dividend":  {resolve: `SELECT id::text FROM dividends WHERE no = $1::bigint`, norm: trimNo, valid: wholeNo},
	"piece":     {resolve: `SELECT id::text FROM item_pieces WHERE serial = $1`, norm: strings.TrimSpace, valid: serialCode},
	"tx":        {norm: lower, valid: uuidText},
	"account":   {norm: lower, valid: uuidText},
	"action":    {norm: lower, valid: uuidText},
	"reference": {norm: lower, valid: uuidText},
	"item":      {norm: lower, valid: itemCode},
	"jurisdiction": {resolve: `SELECT id::text FROM jurisdictions WHERE kind || ':' || code = $1`, norm: lower,
		valid: regexp.MustCompile(`^(city|country|world):[a-z0-9_.-]{1,64}$`)},
}
