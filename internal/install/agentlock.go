package install

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/platform"
)

// lockAgentDir takes the exclusive lock a running agent holds on
// dir/AgentLockName, without waiting, and returns its descriptor; closing
// it releases the lock.
func lockAgentDir(dir string) (int, error) {
	path := filepath.Join(dir, AgentLockName)
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return -1, fmt.Errorf("open %s: %w", path, err)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = unix.Close(fd)
		if errors.Is(err, unix.EWOULDBLOCK) {
			return -1, fmt.Errorf("an agent still runs and holds %s; stop it first", path)
		}
		return -1, fmt.Errorf("lock %s: %w", path, err)
	}
	return fd, nil
}

// waitStopped polls unit every poll until it is inactive or failed, for at
// most timeout.
func waitStopped(ctx context.Context, sd platform.Systemd, unit string, poll, timeout time.Duration) error {
	wait, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		st, err := sd.State(wait, unit)
		if err != nil {
			return err
		}
		if st.ActiveState == "inactive" || st.ActiveState == "failed" {
			return nil
		}
		select {
		case <-wait.Done():
			return fmt.Errorf("%s is still %s after %s", unit, st.ActiveState, timeout)
		case <-time.After(poll):
		}
	}
}
