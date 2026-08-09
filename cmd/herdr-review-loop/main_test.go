package main

import (
	"errors"
	"testing"
)

func TestRunBuiltInCommandsAndUsageErrors(t *testing.T) {
	if err := run([]string{"version"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"help"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"version", "extra"}, {"review", "--unknown"}, {"unknown"}} {
		if err := run(args); !errors.Is(err, errUsage) {
			t.Errorf("run(%q) = %v, want usage error", args, err)
		}
	}
}
