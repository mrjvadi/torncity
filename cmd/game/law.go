package main

import (
	"context"
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/settlementcfg"
	"github.com/mrjvadi/torncity/internal/switches"
)

// matureSource adapts the database's read of a switch to switches.Reader.
type matureSource struct{ ops *postgres.SwitchOps }

func (s matureSource) Get(ctx context.Context, key string) (string, bool, error) {
	return s.ops.Get(ctx, key)
}

// lawRules are the rules of police, courts, informants, the hidden board, laundering and customs (docs/adr/0078), with the global operator switch of
// the mature content read through a short cache. The switch is read before a unit of work opens, never inside one.
func lawRules(cfg *config.Config, pool *postgres.Pool) application.LawRules {
	r := settlementcfg.Law(cfg.Law)
	reader := &switches.Reader{Source: matureSource{postgres.NewSwitchOps(pool)}, TTL: 30 * time.Second}
	r.Mature = func(ctx context.Context) bool { return switches.MatureEnabled(ctx, reader) }
	return r
}
