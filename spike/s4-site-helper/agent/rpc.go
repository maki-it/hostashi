package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"

	rpc "github.com/maki-it/hostashi/spike/s1-agent-rpc/agentrpc"
	"github.com/maki-it/hostashi/spike/s4-site-helper/confine"
	"github.com/maki-it/hostashi/spike/s4-site-helper/helper"
)

// InlineMax keeps file content inside one S1 frame (64 KiB, base64 inflates by 4/3).
// Larger transfers need a separate data path, see REPORT.md.
const InlineMax = 32 << 10

// Files wires the typed file operations into the S1 server.
type Files struct {
	Reg *Registry
	Run *helper.Runner
	Log *slog.Logger
}

// site and path travel in every request; both are validated before anything else happens.
type target struct {
	Site string `json:"site"`
	Path string `json:"path"`
}

func (t target) validate(allowRoot bool) error {
	if err := rpc.ValidateUser(t.Site); err != nil {
		return err
	}
	p, err := confine.ParseRel(t.Path)
	if err != nil {
		return err
	}
	if p.IsRoot() && !allowRoot {
		return errors.New("path: required")
	}
	return nil
}

type StatReq struct{ target }

func (r StatReq) Validate() error { return r.target.validate(true) }

type StatResp struct {
	Info confine.Info `json:"info"`
}

type ListReq struct {
	target
	After string `json:"after,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

func (r ListReq) Validate() error { return r.target.validate(true) }

type ListResp struct {
	Entries   []confine.Info `json:"entries"`
	Truncated bool           `json:"truncated"`
}

type ReadReq struct{ target }

func (r ReadReq) Validate() error { return r.target.validate(false) }

type ReadResp struct {
	Data []byte `json:"data"` // base64 in JSON
}

type WriteReq struct {
	target
	Data      []byte `json:"data"`
	Mode      uint32 `json:"mode"`
	Overwrite bool   `json:"overwrite,omitempty"`
}

func (r WriteReq) Validate() error {
	if len(r.Data) > InlineMax {
		return fmt.Errorf("data: more than %d bytes", InlineMax)
	}
	return r.target.validate(false)
}

type MkdirReq struct {
	target
	Mode    uint32 `json:"mode"`
	Parents bool   `json:"parents,omitempty"`
}

func (r MkdirReq) Validate() error { return r.target.validate(false) }

type RemoveReq struct {
	target
	Recursive bool `json:"recursive,omitempty"`
}

func (r RemoveReq) Validate() error { return r.target.validate(false) }

type RenameReq struct {
	target
	To string `json:"to"`
}

func (r RenameReq) Validate() error {
	if err := r.target.validate(false); err != nil {
		return err
	}
	to, err := confine.ParseRel(r.To)
	if err != nil || to.IsRoot() {
		return errors.New("to: invalid path")
	}
	return nil
}

type Empty struct{}

// Register adds the operations. There is no generic "run helper with this request" operation.
func (f *Files) Register(s *rpc.Server) {
	rpc.Register(s, "SiteFileStat", func(ctx context.Context, c rpc.Caller, r StatReq) (StatResp, error) {
		resp, _, err := f.do(ctx, c, r.Site, helper.Request{Op: helper.OpStat, Path: r.Path}, nil)
		if err != nil {
			return StatResp{}, err
		}
		return StatResp{Info: *resp.Info}, nil
	})
	rpc.Register(s, "SiteFileList", func(ctx context.Context, c rpc.Caller, r ListReq) (ListResp, error) {
		resp, _, err := f.do(ctx, c, r.Site, helper.Request{Op: helper.OpList, Path: r.Path, After: r.After, Limit: r.Limit}, nil)
		if err != nil {
			return ListResp{}, err
		}
		return ListResp{Entries: resp.Entries, Truncated: resp.Truncated}, nil
	})
	rpc.Register(s, "SiteFileRead", func(ctx context.Context, c rpc.Caller, r ReadReq) (ReadResp, error) {
		resp, body, err := f.do(ctx, c, r.Site, helper.Request{Op: helper.OpRead, Path: r.Path}, nil)
		if err != nil {
			return ReadResp{}, err
		}
		if resp.BodySize > InlineMax {
			return ReadResp{}, &rpc.Error{Code: confine.CodeTooLarge, Message: fmt.Sprintf("file larger than %d bytes, use the transfer path", InlineMax)}
		}
		return ReadResp{Data: body}, nil
	})
	rpc.Register(s, "SiteFileWrite", func(ctx context.Context, c rpc.Caller, r WriteReq) (Empty, error) {
		_, _, err := f.do(ctx, c, r.Site, helper.Request{Op: helper.OpWrite, Path: r.Path, Mode: r.Mode, Size: int64(len(r.Data)), Overwrite: r.Overwrite}, r.Data)
		return Empty{}, err
	})
	rpc.Register(s, "SiteFileMkdir", func(ctx context.Context, c rpc.Caller, r MkdirReq) (Empty, error) {
		_, _, err := f.do(ctx, c, r.Site, helper.Request{Op: helper.OpMkdir, Path: r.Path, Mode: r.Mode, Parents: r.Parents}, nil)
		return Empty{}, err
	})
	rpc.Register(s, "SiteFileRemove", func(ctx context.Context, c rpc.Caller, r RemoveReq) (Empty, error) {
		_, _, err := f.do(ctx, c, r.Site, helper.Request{Op: helper.OpRemove, Path: r.Path, Recursive: r.Recursive}, nil)
		return Empty{}, err
	})
	rpc.Register(s, "SiteFileRename", func(ctx context.Context, c rpc.Caller, r RenameReq) (Empty, error) {
		_, _, err := f.do(ctx, c, r.Site, helper.Request{Op: helper.OpRename, Path: r.Path, To: r.To}, nil)
		return Empty{}, err
	})
}

// do resolves the site, starts the helper and maps failures to stable RPC errors.
func (f *Files) do(ctx context.Context, c rpc.Caller, user string, req helper.Request, body []byte) (helper.Response, []byte, error) {
	root, id, err := f.Reg.OpenRoot(user)
	switch {
	case errors.Is(err, ErrUnknownSite):
		return helper.Response{}, nil, &rpc.Error{Code: confine.CodeNotFound, Message: "unknown site"}
	case errors.Is(err, ErrPoolUnavailable):
		f.log().Error("pool unavailable", "site", user, "err", err)
		return helper.Response{}, nil, &rpc.Error{Code: confine.CodeUnavailable, Message: "storage pool unavailable"}
	case err != nil:
		f.log().Error("site root rejected", "site", user, "err", err) // detail stays in the log
		return helper.Response{}, nil, err                            // not an *rpc.Error: client sees "internal"
	}
	defer root.Close()

	var out bytes.Buffer
	var in *bytes.Reader
	if body != nil {
		in = bytes.NewReader(body)
	}
	var resp helper.Response
	if in != nil {
		resp, err = f.Run.Run(ctx, id, root, req, in, &out)
	} else {
		resp, err = f.Run.Run(ctx, id, root, req, nil, &out)
	}
	if err != nil {
		f.log().Error("helper failed", "site", user, "op", req.Op, "err", err)
		return helper.Response{}, nil, err
	}
	f.log().Info("file op", "caller_uid", c.UID, "site", user, "op", req.Op, "ok", resp.OK, "code", resp.Code)
	if !resp.OK {
		return resp, nil, &rpc.Error{Code: resp.Code, Message: resp.Message}
	}
	return resp, out.Bytes(), nil
}

func (f *Files) log() *slog.Logger {
	if f.Log != nil {
		return f.Log
	}
	return slog.Default()
}
