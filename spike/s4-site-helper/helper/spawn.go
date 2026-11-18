package helper

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Identity is the site user the helper runs as. IDs come from the agent's own
// registry, never from the web process.
type Identity struct{ UID, GID uint32 }

// Runner starts helpers. Zero values for MinID and Timeout select defaults.
type Runner struct {
	// Exe is the absolute path of a root-owned, non-writable copy of the agent
	// binary (the helper is the same binary, selected by Arg0).
	Exe string
	// Args overrides the default {Arg0}; tests use it for probe modes.
	Args []string
	// MinID is the lowest uid/gid a helper may run as (site users start here).
	MinID   uint32
	Timeout time.Duration
}

var ErrBadIdentity = errors.New("helper: refusing to start with this identity")

// Run executes one request as id with root (an open directory) as fd 3.
// body is sent after the request header (write); a read response body is
// copied to out. The caller keeps ownership of root.
func (r *Runner) Run(ctx context.Context, id Identity, root *os.File, req Request, body io.Reader, out io.Writer) (Response, error) {
	if _, err := req.Validate(); err != nil {
		return Response{}, err
	}
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd, err := r.Command(ctx, id, root, r.Args...)
	if err != nil {
		return Response{}, err
	}
	var stderr limitBuffer
	stderr.max = 4096
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return Response{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Response{}, err
	}
	if err := cmd.Start(); err != nil {
		return Response{}, fmt.Errorf("helper start: %w", err)
	}

	go func() {
		defer stdin.Close()
		if err := writeHeader(stdin, req); err != nil {
			return
		}
		if body != nil {
			_, _ = io.Copy(stdin, io.LimitReader(body, MaxBody+1)) // helper enforces the exact size
		}
	}()

	br := bufio.NewReaderSize(stdout, 32<<10)
	var resp Response
	herr := readHeader(br, MaxRespHdr, &resp)
	if herr == nil && resp.OK && resp.BodySize > 0 && out != nil {
		if resp.BodySize > MaxBody {
			herr = errors.New("helper announced oversized body")
		} else if _, err := io.CopyN(out, br, resp.BodySize); err != nil {
			herr = fmt.Errorf("short body: %w", err)
		}
	}
	_, _ = io.Copy(io.Discard, br)
	werr := cmd.Wait()
	if herr != nil || werr != nil {
		// Log-only detail; callers map this to an opaque internal error.
		return Response{}, fmt.Errorf("helper failed: header=%v wait=%v stderr=%q ctx=%v", herr, werr, stderr.String(), ctx.Err())
	}
	return resp, nil
}

type limitBuffer struct {
	buf bytes.Buffer
	max int
}

func (l *limitBuffer) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room > 0 {
		if len(p) > room {
			l.buf.Write(p[:room])
		} else {
			l.buf.Write(p)
		}
	}
	return len(p), nil
}

func (l *limitBuffer) String() string { return l.buf.String() }

// Command builds the helper process: re-exec of Exe with dropped credentials,
// empty environment, cwd "/", only fds 0-2 and root as fd 3. Exported so tests
// can run probes in exactly the same kind of process.
func (r *Runner) Command(ctx context.Context, id Identity, root *os.File, args ...string) (*exec.Cmd, error) {
	minID := r.MinID
	if minID == 0 {
		minID = 1000
	}
	if id.UID < minID || id.GID < minID {
		return nil, fmt.Errorf("%w: uid %d gid %d below %d", ErrBadIdentity, id.UID, id.GID, minID)
	}
	if !filepath.IsAbs(r.Exe) {
		return nil, errors.New("helper: Exe must be absolute")
	}
	if args == nil {
		args = []string{Arg0}
	}
	cmd := exec.CommandContext(ctx, r.Exe, args...)
	cmd.Env = []string{} // empty, not nil: nothing is inherited
	cmd.Dir = "/"
	if root != nil {
		cmd.ExtraFiles = []*os.File{root} // becomes fd 3; everything else is close-on-exec
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// Applied between fork and exec in the child, so the helper never runs
		// a single instruction with root credentials. Groups is empty but not
		// nil: setgroups(0) clears the agent's supplementary groups.
		Credential: &syscall.Credential{Uid: id.UID, Gid: id.GID, Groups: []uint32{}},
		Setpgid:    true,
	}
	cmd.WaitDelay = time.Second
	return cmd, nil
}
