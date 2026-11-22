// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/files"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
	"github.com/maki-it/hostashi/internal/store"
)

// The file manager (ADR-0040). The panel checks the role, the site grant
// and the request, writes the audit log and passes the request on to the
// site's helper, which runs as the site user behind
// /run/hostashi-files/<site>.sock. File data streams through the panel and
// never passes the agent; the agent only starts the socket (files.open).

// FilesOpener starts the file manager socket of a site and returns its path.
type FilesOpener func(ctx context.Context, site string) (string, error)

// RPCFilesOpener asks the agent over c.
func RPCFilesOpener(c *agentrpc.Client) FilesOpener {
	return func(ctx context.Context, site string) (string, error) {
		r, err := agentapi.FilesOpen.Call(ctx, c, agentapi.FilesOpenRequest{Site: site})

		return r.Socket, err
	}
}

// maxFilesAuditPaths is how many paths of a selection the audit log names;
// it always holds the count.
const maxFilesAuditPaths = 20

// filesBodyTimeout is how long reading a request body may take (an upload
// chunk of 8 MiB on a slow line).
const filesBodyTimeout = 5 * time.Minute

// filesProxy passes requests on to the helpers.
type filesProxy struct {
	open   FilesOpener
	client *http.Client
	sites  *hosting.Service
	aud    auditor
	log    *slog.Logger

	mu      sync.Mutex
	sockets map[string]string // site -> socket path from the agent
}

func newFilesProxy(open FilesOpener, sites *hosting.Service, aud auditor, log *slog.Logger) *filesProxy {
	p := &filesProxy{open: open, sockets: map[string]string{}, sites: sites, aud: aud, log: log}

	p.client = &http.Client{
		Transport: &http.Transport{
			DialContext:           p.dial,
			MaxIdleConnsPerHost:   2,
			IdleConnTimeout:       30 * time.Second,
			ResponseHeaderTimeout: 2 * time.Minute,
			DisableCompression:    true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	return p
}

// dial connects to the helper of the site named in addr ("<site>:80").
// The agent starts the socket and names its path the first time; after
// that the panel connects directly, and asks the agent again only when the
// socket is gone (agent restart, site deleted and created again).
func (p *filesProxy) dial(ctx context.Context, _, addr string) (net.Conn, error) {
	site, _, err := net.SplitHostPort(addr)
	if err != nil || agentapi.ValidateSiteUser(site) != nil {
		return nil, fmt.Errorf("files: bad helper address %q", addr)
	}

	var d net.Dialer

	p.mu.Lock()
	sock, known := p.sockets[site]
	p.mu.Unlock()

	if known {
		if conn, err := d.DialContext(ctx, "unix", sock); err == nil {
			return conn, nil
		}
	}

	sock, err = p.open(ctx, site)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	p.sockets[site] = sock
	p.mu.Unlock()

	return d.DialContext(ctx, "unix", sock)
}

// siteReady answers for a site that is missing or being created or deleted.
func (p *filesProxy) siteReady(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.PathValue("name")

	site, err := p.sites.Get(r.Context(), name)
	if err != nil {
		siteFail(p.log, w, err, "files")

		return "", false
	}

	if site.State != store.SiteActive {
		writeError(w, http.StatusConflict, "site_busy", "the site is being created or deleted")

		return "", false
	}

	if !site.Account {
		siteFail(p.log, w, hosting.ErrSiteType, "files")

		return "", false
	}

	return name, true
}

// forward sends one request to the site's helper and copies the answer.
func (p *filesProxy) forward(w http.ResponseWriter, r *http.Request, site, method, call string, q url.Values, body io.Reader) bool {
	u := "http://" + site + "/v1/" + call
	if len(q) > 0 {
		u += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(r.Context(), method, u, body)
	if err != nil {
		p.log.ErrorContext(r.Context(), "files request", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error")

		return false
	}

	// The server's 30-second limits are for API calls; a download or an
	// upload chunk may take longer. The request's context still ends when
	// the client goes away.
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	if body != nil {
		_ = rc.SetReadDeadline(time.Now().Add(filesBodyTimeout))
	}

	if rg := r.Header.Get("Range"); rg != "" && method == http.MethodGet {
		req.Header.Set("Range", rg)
	}

	res, err := p.client.Do(req)
	if err != nil {
		p.helperFail(w, r, err)

		return false
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		p.helperError(w, res)

		return false
	}

	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "Content-Disposition", "X-Hostashi-Version", "X-Hostashi-Received"} {
		if v := res.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if strings.HasPrefix(res.Header.Get("Content-Type"), "application/x-ndjson") {
		w.Header().Set("X-Accel-Buffering", "no")
	}

	w.WriteHeader(res.StatusCode)

	var err2 error

	if strings.HasPrefix(res.Header.Get("Content-Type"), "application/x-ndjson") {
		// Progress lines (extract) go out as they come; nginx must not
		// hold them back either (X-Accel-Buffering above).
		err2 = copyFlushing(w, rc, res.Body)
	} else {
		_, err2 = io.Copy(w, res.Body)
	}

	if err2 != nil {
		p.log.InfoContext(r.Context(), "files answer cut off", "site", site, "err", err2)
	}

	return true
}

// copyFlushing copies src to w and flushes after every read.
func copyFlushing(w io.Writer, rc *http.ResponseController, src io.Reader) error {
	buf := make([]byte, 32<<10)

	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}

			_ = rc.Flush()
		}

		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return err
		}
	}
}

func (p *filesProxy) helperFail(w http.ResponseWriter, r *http.Request, err error) {
	var ae *agentrpc.Error

	switch {
	case errors.As(err, &ae):
		p.log.WarnContext(r.Context(), "files: agent refused", "code", ae.Code, "message", ae.Message)

		if ae.Code == agentrpc.CodePrecondition {
			writeError(w, http.StatusServiceUnavailable, "files_unavailable", "the file manager is not available on this server")

			return
		}

		writeError(w, http.StatusBadGateway, "files_unavailable", "the file manager could not be started")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "files_timeout", "the file manager did not answer in time")
	case errors.Is(err, context.Canceled):
		// The client went away.
	default:
		p.log.ErrorContext(r.Context(), "files helper", "err", err)
		writeError(w, http.StatusBadGateway, "files_unavailable", "the file manager did not answer")
	}
}

// helperError passes the helper's error on in the panel's error format.
func (p *filesProxy) helperError(w http.ResponseWriter, res *http.Response) {
	var e files.ErrorBody

	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&e); err != nil || e.Error == "" {
		writeError(w, http.StatusBadGateway, "files_unavailable", "the file manager gave no valid answer")

		return
	}

	if v := res.Header.Get("X-Hostashi-Received"); v != "" {
		w.Header().Set("X-Hostashi-Received", v)
	}

	status := res.StatusCode
	if status == http.StatusInternalServerError {
		status = http.StatusBadGateway
	}

	writeError(w, status, "files_"+e.Error, e.Message)
}

// cleanQueryPath reads and checks ?path=.
func cleanQueryPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	p, err := files.CleanPath(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_argument", err.Error())

		return "", false
	}

	return p, true
}

func (p *filesProxy) audit(r *http.Request, pr auth.Principal, action, site string, detail map[string]string) {
	uid := pr.User.ID
	if err := p.aud.Audit(r.Context(), &uid, action, site, detail); err != nil {
		p.log.ErrorContext(r.Context(), "audit files", "action", action, "site", site, "err", err)
	}
}

func selectionDetail(paths []string) map[string]string {
	d := map[string]string{"count": strconv.Itoa(len(paths))}

	shown := paths
	if len(shown) > maxFilesAuditPaths {
		shown = shown[:maxFilesAuditPaths]
	}

	b, _ := json.Marshal(shown)
	d["paths"] = string(b)

	return d
}

// filesCalls are the POST calls and how each is checked and logged.
var filesCalls = map[string]func(files.Request) (map[string]string, error){
	"mkdir": func(q files.Request) (map[string]string, error) { return onePath(q.Path) },
	"touch": func(q files.Request) (map[string]string, error) { return onePath(q.Path) },
	"rename": func(q files.Request) (map[string]string, error) {
		from, err := nonHome(q.From)
		if err != nil {
			return nil, err
		}

		to, err := nonHome(q.To)
		if err != nil {
			return nil, err
		}

		return map[string]string{"from": from, "to": to}, nil
	},
	"move":   selectionTo,
	"copy":   selectionTo,
	"delete": func(q files.Request) (map[string]string, error) { return selection(q.Paths) },
	"upload-start": func(q files.Request) (map[string]string, error) {
		d, err := onePath(q.Path)
		if err == nil && q.Size < 0 {
			err = fmt.Errorf("%w: negative size", files.ErrInvalidPath)
		}

		if err == nil {
			d["size"] = strconv.FormatInt(q.Size, 10)
			d["overwrite"] = strconv.FormatBool(q.Overwrite)
		}

		return d, err
	},
	"upload-cancel": func(q files.Request) (map[string]string, error) { return onePath(q.Path) },
	"extract": func(q files.Request) (map[string]string, error) {
		d, err := onePath(q.Path)
		if err != nil {
			return nil, err
		}

		to, err := files.CleanPath(q.To)
		if err == nil {
			d["to"] = to
			d["overwrite"] = strconv.FormatBool(q.Overwrite)
		}

		return d, err
	},
	"chmod": func(q files.Request) (map[string]string, error) {
		if _, err := files.ParseMode(q.Mode); err != nil {
			return nil, err
		}

		d, err := selection(q.Paths)
		if err == nil {
			d["mode"] = q.Mode
		}

		return d, err
	},
}

func nonHome(p string) (string, error) {
	c, err := files.CleanPath(p)
	if err == nil && c == "" {
		err = files.ErrHome
	}

	return c, err
}

func onePath(p string) (map[string]string, error) {
	c, err := nonHome(p)
	if err != nil {
		return nil, err
	}

	return map[string]string{"path": c}, nil
}

func selection(paths []string) (map[string]string, error) {
	if len(paths) == 0 || len(paths) > files.MaxSelection {
		return nil, fmt.Errorf("%w: select 1 to %d entries", files.ErrInvalidPath, files.MaxSelection)
	}

	clean := make([]string, 0, len(paths))

	for _, p := range paths {
		c, err := nonHome(p)
		if err != nil {
			return nil, err
		}

		clean = append(clean, c)
	}

	return selectionDetail(clean), nil
}

func selectionTo(q files.Request) (map[string]string, error) {
	to, err := files.CleanPath(q.To)
	if err != nil {
		return nil, err
	}

	d, err := selection(q.Paths)
	if err == nil {
		d["to"] = to
	}

	return d, err
}

// fileOpRequest is the body of the POST file operations: files.Request,
// named for the API document. Each operation reads only its own fields.
type fileOpRequest files.Request

// filesOKJSON is the answer of a file operation without a result.
type filesOKJSON struct {
	OK bool `json:"ok"`
}

// fileOps are the POST operations in the API document, in filesCalls.
var fileOps = []struct {
	call, id, summary string
	out               any
	outContentType    string
}{
	{"mkdir", "createFolder", "Create a folder (path)", filesOKJSON{}, ""},
	{"touch", "createFile", "Create an empty file (path)", filesOKJSON{}, ""},
	{"rename", "renameFile", "Rename a file or folder (from, to)", filesOKJSON{}, ""},
	{"move", "moveFiles", "Move files and folders into a folder (paths, to)", filesOKJSON{}, ""},
	{"copy", "copyFiles", "Copy files and folders into a folder (paths, to)", files.CopyResult{}, ""},
	{"delete", "deleteFiles", "Delete files and folders (paths)", filesOKJSON{}, ""},
	{"chmod", "chmodFiles", "Change the mode of files and folders (paths, mode)", filesOKJSON{}, ""},
	{"upload-start", "startUpload", "Start a chunked upload (path, size, overwrite, parents)", files.Upload{}, ""},
	{"upload-cancel", "cancelUpload", "Cancel a chunked upload (path, id)", filesOKJSON{}, ""},
	{"extract", "extractArchive", "Extract a ZIP or tar archive (path, to, overwrite); one JSON line per progress step", nil, "application/x-ndjson"},
}

var filePathParam = openapi.Param{Name: "path", Description: "relative to the site's home; empty for the home itself"}

// registerFiles adds the file manager routes of a site (ADR-0040).
func registerFiles(mux *routeMux, a authn, px *filesProxy) {
	mux.Op("GET /api/v1/sites/{name}/files", openapi.Operation{
		ID: "listFiles", Tag: "files", Summary: "List a folder", Out: files.Listing{}, Query: []openapi.Param{filePathParam},
	}, a.allow("files:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		path, ok := cleanQueryPath(w, r)
		if !ok {
			return
		}

		if site, ok := px.siteReady(w, r); ok {
			px.forward(w, r, site, http.MethodGet, "list", url.Values{"path": {path}}, nil)
		}
	}))

	mux.Op("GET /api/v1/sites/{name}/files/stat", openapi.Operation{
		ID: "statFile", Tag: "files", Summary: "Get one file or folder", Out: files.Entry{}, Query: []openapi.Param{filePathParam},
	}, a.allow("files:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		path, ok := cleanQueryPath(w, r)
		if !ok {
			return
		}

		if site, ok := px.siteReady(w, r); ok {
			px.forward(w, r, site, http.MethodGet, "stat", url.Values{"path": {path}}, nil)
		}
	}))

	// Downloads are logged as one entry per request (ADR-0040).
	mux.Op("GET /api/v1/sites/{name}/files/content", openapi.Operation{
		ID: "downloadFile", Tag: "files", Summary: "Download a file; X-Hostashi-Version names its version",
		OutContentType: "application/octet-stream", Query: []openapi.Param{filePathParam},
	}, a.allow("files:read", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		path, ok := cleanQueryPath(w, r)
		if !ok {
			return
		}

		site, ok := px.siteReady(w, r)
		if !ok {
			return
		}

		if px.forward(w, r, site, http.MethodGet, "file", url.Values{"path": {path}}, nil) {
			px.audit(r, p, "files.download", site, map[string]string{"path": path})
		}
	}))

	// Saving from the editor: ?version= is the version the editor opened;
	// without it the file must not exist yet.
	mux.Op("PUT /api/v1/sites/{name}/files/content", openapi.Operation{
		ID: "saveFile", Tag: "files", Summary: "Save a file from the editor", InContentType: "application/octet-stream", Out: files.Entry{},
		Query: []openapi.Param{filePathParam, {Name: "version", Description: "the version the editor opened; without it the file must not exist yet"}},
	}, a.allow("files:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		path, ok := cleanQueryPath(w, r)
		if !ok {
			return
		}

		if path == "" {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", files.ErrHome.Error())

			return
		}

		site, ok := px.siteReady(w, r)
		if !ok {
			return
		}

		q := url.Values{"path": {path}}
		if v := r.URL.Query().Get("version"); v != "" {
			q.Set("version", v)
		}

		body := http.MaxBytesReader(w, r.Body, files.MaxEditBytes+1)

		if px.forward(w, r, site, http.MethodPut, "file", q, body) {
			px.audit(r, p, "files.save", site, map[string]string{"path": path})
		}
	}))

	// Several files and folders as one ZIP, built while streaming.
	mux.Op("GET /api/v1/sites/{name}/files/zip", openapi.Operation{
		ID: "downloadZip", Tag: "files", Summary: "Download files and folders as one ZIP", OutContentType: "application/zip",
		Query: []openapi.Param{{Name: "path", Description: "an entry to include, relative to the site's home", Required: true, Repeated: true}},
	}, a.allow("files:read", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		paths := r.URL.Query()["path"]

		detail, err := selection(paths)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", err.Error())

			return
		}

		site, ok := px.siteReady(w, r)
		if !ok {
			return
		}

		if px.forward(w, r, site, http.MethodGet, "zip", url.Values{"path": paths}, nil) {
			detail["zip"] = "true"
			px.audit(r, p, "files.download", site, detail)
		}
	}))

	// One chunk of an upload (8 MiB at most); upload-start is in the audit
	// log, the chunks are not.
	mux.Op("PUT /api/v1/sites/{name}/files/upload", openapi.Operation{
		ID: "uploadChunk", Tag: "files", Summary: "Send one chunk (at most 8 MiB) of an upload", InContentType: "application/octet-stream", Out: files.Upload{},
		Query: []openapi.Param{
			filePathParam,
			{Name: "id", Required: true},
			{Name: "offset", Type: "integer", Required: true},
			{Name: "size", Type: "integer", Required: true},
			{Name: "overwrite", Description: "1 to replace an existing file"},
		},
	}, a.allow("files:write", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		path, ok := cleanQueryPath(w, r)
		if !ok {
			return
		}

		in := r.URL.Query()
		q := url.Values{"path": {path}, "id": {in.Get("id")}, "offset": {in.Get("offset")}, "size": {in.Get("size")}}

		if in.Get("overwrite") == "1" {
			q.Set("overwrite", "1")
		}

		site, ok := px.siteReady(w, r)
		if !ok {
			return
		}

		px.forward(w, r, site, http.MethodPut, "upload", q, http.MaxBytesReader(w, r.Body, files.MaxChunk+1))
	}))

	for _, o := range fileOps {
		mux.Op("POST /api/v1/sites/{name}/files/"+o.call, openapi.Operation{
			ID: o.id, Tag: "files", Summary: o.summary, In: fileOpRequest{}, Out: o.out, OutContentType: o.outContentType,
		}, a.allow("files:write", fileOp(px, o.call, filesCalls[o.call])))
	}
}

// fileOp runs one POST file operation: check, pass on, log.
func fileOp(px *filesProxy, call string, check func(files.Request) (map[string]string, error)) func(http.ResponseWriter, *http.Request, auth.Principal) {
	return func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var req files.Request
		if !decodeJSON(w, r, &req) {
			return
		}

		detail, err := check(req)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", err.Error())

			return
		}

		site, ok := px.siteReady(w, r)
		if !ok {
			return
		}

		b, err := json.Marshal(req)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "internal error")

			return
		}

		if px.forward(w, r, site, http.MethodPost, call, nil, bytes.NewReader(b)) {
			action := "files." + strings.ToLower(call)
			if call == "upload-start" {
				action = "files.upload"
			}

			px.audit(r, p, action, site, detail)
		}
	}
}
