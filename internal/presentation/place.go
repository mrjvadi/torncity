package presentation

// GovPlace is one jurisdiction: a city or a country, by kind and code, with
// its authored name as the fallback for a code an edge has no name for.
type GovPlace struct {
	Kind string
	Code string
	// Name is the authored name, the fallback for an untranslated code.
	Name string
}
