package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	natsgo "github.com/nats-io/nats.go"

	"github.com/mrjvadi/torncity/internal/clientapi"
	"github.com/mrjvadi/torncity/internal/config"
	"github.com/mrjvadi/torncity/internal/content"
	"github.com/mrjvadi/torncity/internal/domain/player"
	infranats "github.com/mrjvadi/torncity/internal/infrastructure/nats"
	"github.com/mrjvadi/torncity/internal/infrastructure/postgres"
	"github.com/mrjvadi/torncity/internal/messaging/nats/envelope"
	"github.com/mrjvadi/torncity/internal/messaging/nats/subjects"
	"github.com/mrjvadi/torncity/internal/statesync"
)

// The client state sync projector (docs/adr/0034-client-state-sync.md)
// runs in this process, beside the notices: it reads the same event stream,
// on its own durable consumer (state_projector), so N notifier replicas
// share the projection work the way they share the notices. Per-player
// order does not come from the consumer (any replica takes any event) but
// from the per-player lock every projection takes (postgres.StateSync).

// projectorDurable is the projector's consumer on the event stream, and its
// queue group on the response subjects.
const projectorDurable = "state_projector"

// newStateSync builds the service, or nil when state_sync.enabled is off.
func newStateSync(ctx context.Context, cfg *config.Config, pool *postgres.Pool, pub statesync.Publisher,
	logger *slog.Logger,
) *statesync.Service {
	if !cfg.StateSync.Enabled {
		logger.Info("state sync is off (state_sync.enabled)")
		return nil
	}
	// The settlement summary carries the layout's version for each viewer,
	// computed by the very code the layout endpoint uses, which needs the
	// buildings' footprints from the active content.
	registry := content.NewRegistry()
	contentStore := postgres.NewContentStore(pool)
	reloadContent(ctx, contentStore, registry, logger)
	go func() {
		t := time.NewTicker(cfg.Game.ContentReloadInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				reloadContent(ctx, contentStore, registry, logger)
			}
		}
	}()
	villages := &clientapi.VillageService{Settlements: postgres.NewSettlementReader(pool),
		Buildings: postgres.NewSettlementBuildingReader(pool), Content: registry,
		VillageGridLots: cfg.Settlement.VillageGridLots, Citizens: postgres.NewCitizenReader(pool),
		Overlay: postgres.NewVillageFacts(pool), StockBaseCapacity: cfg.Settlement.StockBaseCapacity}
	store := postgres.NewStateSync(pool, stateRules(cfg))
	store.Layouts = villages
	store.Overlays = villages
	store.LockTimeout = cfg.StateSync.LockTimeout
	return &statesync.Service{Store: store, Pub: pub, Cfg: statesync.FromConfig(cfg.StateSync),
		Metrics: statesync.NewMetrics(), Log: logger.With(slog.String("component", "state_sync"))}
}

// stateRules are the numbers vitals are projected with.
func stateRules(cfg *config.Config) postgres.StateRules {
	return postgres.StateRules{
		EnergyRegenAmount: player.EnergyRegenAmount, EnergyRegenInterval: player.EnergyRegenInterval,
		NerveMax: cfg.Crime.NerveMax, NerveRegenAmount: cfg.Crime.NerveRegenAmount, NerveRegenInterval: cfg.Crime.NerveRegenInterval,
		NoticesKept: cfg.StateSync.NoticesKept,
	}
}

// runStateSync starts the projector's consumer, the poke after every
// command, the trim and the metrics line. It returns once they run; they
// stop with ctx.
func runStateSync(ctx context.Context, cfg *config.Config, svc *statesync.Service, consumer *infranats.Consumer,
	nc *natsgo.Conn, logger *slog.Logger,
) error {
	if err := consumer.Subscribe(ctx, subjects.EventStream, projectorDurable, svc.HandleEvent); err != nil {
		return err
	}
	logger.Info("consuming", slog.String("subject", subjects.EventStream), slog.String("consumer", projectorDurable))

	// The poke after any command: the game publishes its answer after the
	// command committed, on core NATS. A queue group, so one replica takes
	// each; a bounded set of workers, so a burst of commands cannot open
	// more projections at once than the pool can serve (a poke that finds
	// them all busy is dropped: the next projection of that player, by an
	// event, a command or a snapshot, catches it up).
	workers := max(cfg.Postgres.MaxConns/2, 1)
	pokes := make(chan envelope.Metadata, workers*4)
	for range workers {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case meta := <-pokes:
					pctx, cancel := context.WithTimeout(ctx, cfg.StateSync.LockTimeout+cfg.Realtime.PublishTimeout)
					svc.HandleResponse(pctx, meta)
					cancel()
				}
			}
		}()
	}
	sub, err := nc.QueueSubscribe(subjects.Response("*"), projectorDurable, func(m *natsgo.Msg) {
		var env envelope.Envelope
		if json.Unmarshal(m.Data, &env) != nil {
			return
		}
		select {
		case pokes <- env.Metadata:
		default:
		}
	})
	if err != nil {
		return errors.Join(errors.New("notifier: subscribing to command answers for state sync"), err)
	}
	go func() {
		<-ctx.Done()
		_ = sub.Unsubscribe()
	}()

	go svc.Trimmer(ctx, cfg.StateSync.TrimInterval)
	go svc.Reporter(ctx, cfg.StateSync.MetricsInterval)
	return nil
}

// recordedHook is what the notification worker calls after it stored a
// notice: the player is projected again so the notice reaches the client.
func recordedHook(svc *statesync.Service, timeout time.Duration) func(ctx context.Context, playerID string, meta envelope.Metadata) {
	if svc == nil {
		return nil
	}
	return func(ctx context.Context, playerID string, meta envelope.Metadata) {
		pctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		if _, err := svc.Project(pctx, playerID, "ntc:"+meta.MessageID(), meta.RequestID,
			statesync.Kinds(statesync.KindInbox, statesync.KindNotice)); err != nil {
			svc.Log.Warn("cannot project a stored notice", slog.String("player_id", playerID), slog.String("error", err.Error()))
		}
	}
}

// reloadContent swaps in the active content version when it changed, as
// cmd/clientapi does.
func reloadContent(ctx context.Context, store *postgres.ContentStore, registry *content.Registry, logger *slog.Logger) {
	active, err := store.Active(ctx)
	if errors.Is(err, postgres.ErrNoActiveVersion) || (err == nil && active.Version == registry.Version()) {
		return
	}
	if err != nil {
		logger.Warn("cannot check the active content version", slog.String("error", err.Error()))
		return
	}
	pack, err := store.LoadActive(ctx)
	if err == nil {
		var snap *content.Snapshot
		if snap, err = content.BuildSnapshot(pack.Version, pack); err == nil {
			registry.Swap(snap)
			return
		}
	}
	logger.Error("the active content version cannot be served", slog.String("error", err.Error()))
}
