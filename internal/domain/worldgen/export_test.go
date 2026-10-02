package worldgen

// SmallWorldForTests generates the fast fixture world (seed given) for tests in
// packages that build on worldgen (roads, territory): the same content and
// parameters this package's own tests use. Test-only.
func SmallWorldForTests(seed uint64) (*World, error) {
	return Generate(seed, smallParams(), sampleContent())
}
