package helper

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Harden is the first thing the helper does. It refuses to run with any
// privilege left and removes what a compromised helper could still use.
// The agent drops credentials (setgroups, setgid, setuid) between fork and
// exec via SysProcAttr.Credential; this function checks the result instead of
// trusting it.
func Harden() error {
	r, e, s := unix.Getresuid()
	if r == 0 || e == 0 || s == 0 || r != e || e != s {
		return fmt.Errorf("helper: uids %d/%d/%d: refusing to run as or with a path back to root", r, e, s)
	}
	r, e, s = unix.Getresgid()
	if r == 0 || e == 0 || s == 0 || r != e || e != s {
		return fmt.Errorf("helper: gids %d/%d/%d: refusing to run with root group", r, e, s)
	}
	groups, err := unix.Getgroups()
	if err != nil || len(groups) != 0 {
		return fmt.Errorf("helper: supplementary groups present (%v, err %v)", groups, err)
	}
	// Nothing, including a setuid or file-capability binary, may gain privilege from here on.
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("helper: no_new_privs: %w", err)
	}
	// Not ptrace-able or core-dumpable by other processes of the same uid.
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("helper: dumpable: %w", err)
	}
	for res, v := range map[int]uint64{unix.RLIMIT_CORE: 0, unix.RLIMIT_NOFILE: 64} {
		if err := unix.Setrlimit(res, &unix.Rlimit{Cur: v, Max: v}); err != nil {
			return fmt.Errorf("helper: rlimit %d: %w", res, err)
		}
	}
	// Explicit modes only: files get exactly the mode that was requested and validated.
	unix.Umask(0)
	return nil
}

// RootFD is the descriptor number the agent passes the site root directory on.
const RootFD = 3

// checkRootOwner is the helper's own check of what the agent handed over: the
// directory must belong to the uid we run as.
func checkRootOwner(fd int) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return errors.New("fd 3 is not a directory")
	}
	if int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("root directory owned by uid %d, helper runs as %d", st.Uid, os.Geteuid())
	}
	return nil
}
