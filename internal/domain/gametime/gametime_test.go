package gametime

import (
	"errors"
	"testing"
	"time"
)

func TestRealWait(t *testing.T) {
	tests := []struct {
		game  time.Duration
		scale Scale
		want  time.Duration
	}{
		{24 * time.Hour, 60, 24 * time.Minute},
		{8 * time.Hour, 60, 8 * time.Minute},
		{90 * time.Minute, 60, 90 * time.Second},
		{3*time.Hour + 30*time.Second, 60, 3*time.Minute + time.Second}, // 180.5s rounds up
		{time.Second, 60, time.Second},                                  // floor of one second
		{time.Nanosecond, 1, time.Second},
		{2 * time.Hour, 1, 2 * time.Hour},
		{0, 60, 0},
		{-time.Hour, 60, 0},
		{time.Hour, MaxScale, time.Second},
		{time.Hour, 0, time.Hour},            // invalid scale reads as 1
		{time.Hour, MaxScale + 1, time.Hour}, // invalid scale reads as 1
	}
	for _, tc := range tests {
		if got := tc.scale.RealWait(tc.game); got != tc.want {
			t.Errorf("Scale(%d).RealWait(%s) = %s, want %s", int(tc.scale), tc.game, got, tc.want)
		}
	}
}

func TestValidate(t *testing.T) {
	for _, s := range []Scale{1, 60, MaxScale} {
		if err := s.Validate(); err != nil {
			t.Errorf("Scale(%d).Validate() = %v", int(s), err)
		}
	}
	for _, s := range []Scale{0, -1, MaxScale + 1} {
		if err := s.Validate(); !errors.Is(err, ErrInvalidScale) {
			t.Errorf("Scale(%d).Validate() = %v, want ErrInvalidScale", int(s), err)
		}
	}
}
