package main

import (
	"reflect"
	"testing"

	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/domain/labor"
)

// The shipped configuration is a valid rule set, and is the one the domain
// package's own default documents.
func TestLaborRulesFromTheShippedConfig(t *testing.T) {
	got := laborRules(config.Defaults().Labor)
	if err := got.Validate(); err != nil {
		t.Fatalf("the default labour configuration is invalid: %v", err)
	}
	if want := labor.Default(); !reflect.DeepEqual(got, want) {
		t.Errorf("config defaults and labor.Default() differ:\n%+v\n%+v", got, want)
	}
}
