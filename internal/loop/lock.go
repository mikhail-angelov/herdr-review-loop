package loop

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

type LockRecord struct {
	PID            int       `json:"pid"`
	Workspace      string    `json:"workspace"`
	Started        time.Time `json:"started"`
	ProcessStarted string    `json:"process_started"`
}
type Lock struct {
	file     *os.File
	Record   LockRecord
	stateDir string
}

func lockPath(dir string) string   { return filepath.Join(dir, "run.lock") }
func recordPath(dir string) string { return filepath.Join(dir, "run.lock.json") }

func AcquireLock(stateDir, workspace string) (*Lock, error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(lockPath(stateDir), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("another review loop is already running")
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
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	_ = os.Remove(recordPath(l.stateDir))
	unlock := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if unlock != nil {
		return unlock
	}
	return closeErr
}
func Holder(stateDir string) (LockRecord, error) {
	data, err := os.ReadFile(recordPath(stateDir))
	if err != nil {
		return LockRecord{}, err
	}
	var record LockRecord
	if err = json.Unmarshal(data, &record); err != nil || record.PID <= 0 || record.ProcessStarted == "" {
		return LockRecord{}, fmt.Errorf("invalid lock record")
	}
	return record, nil
}
func StillTheHolder(record LockRecord) bool {
	started, err := processStart(record.PID)
	return err == nil && started == record.ProcessStarted
}
func processStart(pid int) (string, error) {
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return "", fmt.Errorf("process %d is not running", pid)
	}
	return value, nil
}
func writeRecord(path string, record LockRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".run-lock-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err = temp.Write(append(data, '\n')); err == nil {
		err = temp.Close()
	} else {
		_ = temp.Close()
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
