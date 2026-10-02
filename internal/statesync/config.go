package statesync

import "github.com/mrjvadi/torncity/internal/config"

// FromConfig is state_sync.* as the service takes it.
func FromConfig(c config.StateSync) Config {
	return Config{
		Enabled:        c.Enabled,
		Epoch:          c.Epoch,
		ResetThreshold: c.ResetThreshold,
		PullLimit:      c.PullLimit,
		CommandWait:    c.CommandWait,
		PushMaxRecords: c.PushMaxRecords,
		PushMaxBytes:   c.PushMaxBytes,
		MaxEventAge:    c.MaxEventAge,
		PlayerKeys:     append([]string(nil), c.PlayerKeys...),
		FanoutLimit:    c.FanoutLimit,
		CauseWindow:    c.CauseWindow,
		Retention: TrimPolicy{
			Age: c.RetentionAge, Records: c.RetentionRecords, Min: c.RetentionMin, Batch: c.TrimBatch,
		},
	}
}
