package helper

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/spike/s4-site-helper/confine"
)

// Main is the helper entry point: harden, open root from fd 3, serve one request.
// The exit code is non-zero only if the helper could not even start safely.
func Main() int {
	if err := Harden(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 70
	}
	if err := checkRootOwner(RootFD); err != nil {
		fmt.Fprintln(os.Stderr, "helper:", err)
		return 71
	}
	unix.CloseOnExec(RootFD)
	root, err := confine.NewRoot(RootFD, confine.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "helper:", err)
		return 72
	}
	defer root.Close()
	if err := Serve(root, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "helper:", err)
		return 73
	}
	return 0
}

// Serve handles exactly one request. Operation failures are reported in the
// response; the returned error means the protocol itself broke.
func Serve(root *confine.Root, in io.Reader, out io.Writer) error {
	br := bufio.NewReaderSize(in, 32<<10)
	bw := bufio.NewWriterSize(out, 32<<10)
	defer bw.Flush()

	var req Request
	if err := readHeader(br, MaxHeader, &req); err != nil {
		_ = writeHeader(bw, failure(confine.ErrInvalidPath))
		return fmt.Errorf("bad request header: %w", err)
	}
	p, err := req.Validate()
	if err != nil {
		return writeHeader(bw, failure(err))
	}
	switch req.Op {
	case OpStat:
		info, err := root.Stat(p.Path)
		if err != nil {
			return writeHeader(bw, failure(err))
		}
		return writeHeader(bw, Response{OK: true, Info: &info})
	case OpList:
		limit := req.Limit
		if limit == 0 {
			limit = 200
		}
		ents, trunc, err := root.List(p.Path, req.After, limit)
		if err != nil {
			return writeHeader(bw, failure(err))
		}
		return writeHeader(bw, Response{OK: true, Entries: ents, Truncated: trunc})
	case OpRead:
		f, size, err := root.OpenRead(p.Path)
		if err != nil {
			return writeHeader(bw, failure(err))
		}
		defer f.Close()
		if size > MaxBody {
			return writeHeader(bw, failure(&confine.Error{Code: confine.CodeTooLarge, Msg: "file larger than limit"}))
		}
		if err := writeHeader(bw, Response{OK: true, BodySize: size}); err != nil {
			return err
		}
		// The file may grow after fstat: never send more than announced.
		_, err = io.Copy(bw, io.LimitReader(f, size))
		return err
	case OpWrite:
		// Everything after the header line is the body; the writer enforces the exact size.
		err := root.WriteFile(p.Path, req.Mode, br, req.Size, req.Overwrite)
		if err != nil {
			return writeHeader(bw, failure(err))
		}
		return writeHeader(bw, Response{OK: true})
	case OpMkdir:
		return reply(bw, root.Mkdir(p.Path, req.Mode, req.Parents))
	case OpRemove:
		return reply(bw, root.Remove(p.Path, req.Recursive))
	case OpRename:
		return reply(bw, root.Rename(p.Path, p.To))
	case OpChmod:
		return reply(bw, root.Chmod(p.Path, req.Mode))
	}
	return writeHeader(bw, failure(confine.ErrInvalidPath)) // unreachable: Validate covers all ops
}

func reply(w io.Writer, err error) error {
	if err != nil {
		return writeHeader(w, failure(err))
	}
	return writeHeader(w, Response{OK: true})
}
