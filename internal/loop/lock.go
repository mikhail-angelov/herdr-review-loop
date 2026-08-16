package loop

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// LockRecord identifies the process holding the run lock. The start time is recorded alongside the
// pid because pids are recycled, and signaling a stranger is worse than doing nothing.
type LockRecord struct {
	PID            int       `json:"pid"`
	Workspace      string    `json:"workspace"`
	Started        time.Time `json:"started"`
	ProcessStarted string    `json:"process_started"`
}

// Lock is a held run lock. Release must be called; the flock alone would survive a crash.
type Lock struct {
	file     *os.File
	Record   LockRecord
	stateDir string
}

// PanelRecord identifies the process rendering a workspace's panel, with the same pid/start
// identity the run lock uses.
type PanelRecord struct {
	PID            int    `json:"pid"`
	PaneID         string `json:"pane_id"`
	ProcessStarted string `json:"process_started"`
}

func panelPath(dir, workspace string) string { return filepath.Join(dir, "panel."+workspace+".json") }

// ClaimPanel registers this process as the workspace's panel. It reports the existing record and
// false when another live panel already holds the workspace, so the caller can focus it instead of
// opening a second one.
func ClaimPanel(stateDir, workspace, paneID string) (PanelRecord, bool, error) {
	if workspace == "" || paneID == "" {
		return PanelRecord{}, false, errors.New("panel requires a workspace and pane id")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return PanelRecord{}, false, fmt.Errorf("failed to create state directory %s: %w", stateDir, err)
	}
	guard, err := acquirePanelGuard(stateDir, workspace)
	if err != nil {
		return PanelRecord{}, false, err
	}
	defer func() { _ = guard.Close() }()
	record := PanelRecord{PID: os.Getpid(), PaneID: paneID}
	record.ProcessStarted, err = processStart(record.PID)
	if err != nil {
		return PanelRecord{}, false, err
	}
	path := panelPath(stateDir, workspace)
	for attempts := 0; attempts < 2; attempts++ {
		created, createErr := publishPanel(path, record)
		if createErr == nil && created {
			return record, true, nil
		}
		if createErr != nil {
			return PanelRecord{}, false, createErr
		}
		existing, readErr := readPanel(path)
		if readErr == nil && panelAlive(existing) {
			return existing, false, nil
		}
		_ = os.Remove(path)
	}
	return PanelRecord{}, false, errors.New("cannot claim panel record")
}

// The guard exists only while a process reads, clears, and publishes the panel
// record. It does not represent a live panel; the record's pid/start identity
// remains the source of truth after the claim completes.
func acquirePanelGuard(stateDir, workspace string) (*os.File, error) {
	file, err := os.OpenFile(panelPath(stateDir, workspace)+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open panel guard: %w", err)
	}
	if err = unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("failed to lock panel guard: %w", err)
	}
	return file, nil
}

// publishPanel writes a complete record before linking it into place. os.Link is an
// atomic create-if-absent operation, so a competing claim can never observe a
// half-written record and mistake it for a stale panel.
func publishPanel(path string, record PanelRecord) (bool, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return false, fmt.Errorf("failed to encode panel record: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".panel-*")
	if err != nil {
		return false, fmt.Errorf("failed to create temporary panel record: %w", err)
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }()
	if err = temp.Chmod(0o600); err == nil {
		_, err = temp.Write(append(data, '\n'))
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return false, fmt.Errorf("failed to write panel record: %w", err)
	}
	if err = os.Link(name, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("failed to publish panel record: %w", err)
	}
	return true, nil
}

// LivePanel returns the workspace's panel record while its process is still running, clearing the
// record when it is not.
func LivePanel(stateDir, workspace string) (PanelRecord, bool) {
	guard, err := acquirePanelGuard(stateDir, workspace)
	if err != nil {
		return PanelRecord{}, false
	}
	defer func() { _ = guard.Close() }()
	record, err := readPanel(panelPath(stateDir, workspace))
	if err != nil {
		return PanelRecord{}, false
	}
	if panelAlive(record) {
		return record, true
	}
	_ = os.Remove(panelPath(stateDir, workspace))
	return PanelRecord{}, false
}

// ClosePanel terminates the workspace's panel, which ends its pane. The pid is
// signaled only while its recorded start time still matches, for the reason
// stop re-checks before every signal: a recycled pid names a stranger.
func ClosePanel(stateDir, workspace string) bool {
	record, live := LivePanel(stateDir, workspace)
	if !live {
		return false
	}
	process, err := os.FindProcess(record.PID)
	if err != nil {
		return false
	}
	if !panelAlive(record) {
		return false
	}
	_ = process.Signal(unix.SIGTERM)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && panelAlive(record) {
		time.Sleep(100 * time.Millisecond)
	}
	return true
}

func readPanel(path string) (PanelRecord, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is built from the plugin's own state directory
	if err != nil {
		return PanelRecord{}, fmt.Errorf("failed to read panel record: %w", err)
	}
	var record PanelRecord
	if err = json.Unmarshal(data, &record); err != nil || record.PID <= 0 || record.PaneID == "" || record.ProcessStarted == "" {
		return PanelRecord{}, errors.New("invalid panel record")
	}
	return record, nil
}
func panelAlive(record PanelRecord) bool {
	started, err := processStart(record.PID)
	return err == nil && started == record.ProcessStarted
}

func lockPath(dir string) string   { return filepath.Join(dir, "run.lock") }
func recordPath(dir string) string { return filepath.Join(dir, "run.lock.json") }

// ErrLocked reports that someone else holds the run lock, so a caller that has
// its own words for that condition can recognize it without matching a string.
var ErrLocked = errors.New("another review loop is already running")

// AcquireLock takes the exclusive run lock, so only one review loop runs per state directory.
func AcquireLock(stateDir, workspace string) (*Lock, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create state directory %s: %w", stateDir, err)
	}
	file, err := os.OpenFile(lockPath(stateDir), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file: %w", err)
	}
	if err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, ErrLocked
	}
	started, err := processStart(os.Getpid())
	if err != nil {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
		return nil, err
	}

	lock := &Lock{file: file, stateDir: stateDir, Record: LockRecord{PID: os.Getpid(), Workspace: workspace, Started: time.Now().UTC(), ProcessStarted: started}}
	if err := writeRecord(recordPath(stateDir), lock.Record); err != nil {
		_ = lock.Release()
		return nil, err
	}
	return lock, nil
}

// Release publishes nothing, removes the record and drops the flock. Safe on a nil lock.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	_ = os.Remove(recordPath(l.stateDir))
	unlock := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if unlock != nil {
		return fmt.Errorf("failed to unlock run lock: %w", unlock)
	}
	if closeErr != nil {
		return fmt.Errorf("failed to close run lock: %w", closeErr)
	}
	return nil
}

// Holder reads the published run-lock record, without checking whether its process still exists.
func Holder(stateDir string) (LockRecord, error) {
	data, err := os.ReadFile(recordPath(stateDir))
	if err != nil {
		return LockRecord{}, fmt.Errorf("failed to read lock record: %w", err)
	}
	var record LockRecord
	if err = json.Unmarshal(data, &record); err != nil || record.PID <= 0 || record.ProcessStarted == "" {
		return LockRecord{}, errors.New("invalid lock record")
	}
	return record, nil
}

// WaitHolder distinguishes an unlocked state from the short interval where a holder
// has acquired flock but has not atomically published its record yet.
func WaitHolder(stateDir string, timeout time.Duration) (LockRecord, bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		record, err := Holder(stateDir)
		if err == nil {
			return record, true, nil
		}
		held, lockErr := IsHeld(stateDir)
		if lockErr != nil {
			return LockRecord{}, false, lockErr
		}
		if !held {
			return LockRecord{}, false, nil
		}
		if time.Now().After(deadline) {
			return LockRecord{}, true, errors.New("a review loop is starting or its record is unreadable — try again")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// IsHeld reports whether any process currently holds the run lock's flock.
func IsHeld(stateDir string) (bool, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return false, fmt.Errorf("failed to create state directory %s: %w", stateDir, err)
	}
	file, err := os.OpenFile(lockPath(stateDir), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, fmt.Errorf("failed to open lock file: %w", err)
	}
	defer func() { _ = file.Close() }()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_SH|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return true, nil
		}
		return false, fmt.Errorf("failed to test run lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_UN); err != nil {
		return false, fmt.Errorf("failed to release run lock test: %w", err)
	}
	return false, nil
}

// StillTheHolder reports whether the recorded pid is the same process that took the lock.
func StillTheHolder(record LockRecord) bool {
	started, err := processStart(record.PID)
	return err == nil && started == record.ProcessStarted
}

// LiveRun reports whether a review loop is running right now.
func LiveRun(stateDir string) bool {
	record, err := Holder(stateDir)
	return err == nil && StillTheHolder(record)
}
func processStart(pid int) (string, error) {
	// ps is the portable way to read a process start time on both Linux and macOS; the only
	// argument is a pid this process produced
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output() //nolint:gosec // pid is ours
	if err != nil {
		return "", fmt.Errorf("failed to read start time of process %d: %w", pid, err)
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return "", fmt.Errorf("process %d is not running", pid)
	}
	return value, nil
}
func writeRecord(path string, record LockRecord) error { return writeJSON(path, record) }
