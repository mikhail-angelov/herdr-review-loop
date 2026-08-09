package config

import (
	"testing"
	"time"
)

func TestShowDuration(t *testing.T) {
	for input, want := range map[time.Duration]string{30 * time.Minute: "30m", 10 * time.Minute: "10m", 90 * time.Second: "1m30s", time.Hour: "1h", 150 * time.Minute: "2h30m"} {
		if got := showDuration(input); got != want {
			t.Errorf("showDuration(%s)=%q, want %q", input, got, want)
		}
	}
}
