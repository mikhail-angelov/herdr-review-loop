package loop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestAcquireLockRefusesSecondHolder(t *testing.T) {
	directory := t.TempDir()
	lock, err := AcquireLock(directory, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	if _, err := AcquireLock(directory, "workspace"); err == nil {
		t.Fatal("acquired a second lock")
	}
}

func TestConcurrentPanelClaimsReplaceStaleRecordOnce(t *testing.T) {
	directory := t.TempDir()
	stale, err := json.Marshal(PanelRecord{PID: os.Getpid(), PaneID: "old", ProcessStarted: "not-this-process"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "panel.workspace.json"), stale, 0o600); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan bool, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, won, err := ClaimPanel(directory, "workspace", "pane")
			if err != nil {
				t.Errorf("ClaimPanel: %v", err)
				return
			}
			results <- won
		}()
	}
	close(start)
	group.Wait()
	close(results)
	winners := 0
	for won := range results {
		if won {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("got %d winners, want 1", winners)
	}
}

func TestLivePanelClearsStaleRecord(t *testing.T) {
	directory := t.TempDir()
	stale, err := json.Marshal(PanelRecord{PID: os.Getpid(), PaneID: "old", ProcessStarted: "not-this-process"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "panel.workspace.json")
	if err := os.WriteFile(path, stale, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, live := LivePanel(directory, "workspace"); live {
		t.Fatal("stale record reported live")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale record remains: %v", err)
	}
}

func TestConcurrentPanelClaimsHaveOneWinner(t *testing.T) {
	directory := t.TempDir()
	start := make(chan struct{})
	results := make(chan bool, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, won, err := ClaimPanel(directory, "workspace", "pane")
			if err != nil {
				t.Errorf("ClaimPanel: %v", err)
				return
			}
			results <- won
		}()
	}
	close(start)
	group.Wait()
	close(results)
	winners := 0
	for won := range results {
		if won {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("got %d winners, want 1", winners)
	}
}
