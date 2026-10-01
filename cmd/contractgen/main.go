// Command contractgen writes the TypeScript types of the neutral views
// (internal/presentation/tsgen) to standard output:
//
//	go run ./cmd/contractgen > api/views.gen.ts
//
// A test (internal/presentation/tsgen) fails when api/views.gen.ts is out of
// date, so the web client's types are the Go views' types.
package main

import (
	"fmt"

	"github.com/mrjvadi/torncity/internal/presentation"
	_ "github.com/mrjvadi/torncity/internal/presentation/life"
	_ "github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/presentation/tsgen"
)

func main() {
	fmt.Print(tsgen.Generate(presentation.Specs()))
}
