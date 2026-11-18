package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// terminal bridges a WebSocket to a PTY shell. SPIKE ONLY: the product must
// never start a shell from the web process. There the terminal would be an
// agent operation that starts a helper as the site user (see S1/S4).
//
// Protocol: binary frames carry terminal bytes; text frames carry JSON
// control messages, currently {"type":"resize","cols":N,"rows":N}.
func terminal(w http.ResponseWriter, r *http.Request) {
	// Accept checks that Origin matches Host, which blocks cross-site
	// WebSocket hijacking from other pages.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/bash", "--norc", "--noprofile")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "PS1=$ ")
	f, err := pty.Start(cmd)
	if err != nil {
		log.Printf("pty: %v", err)
		c.Close(websocket.StatusInternalError, "pty failed")
		return
	}
	defer func() { _ = f.Close(); _ = cmd.Wait() }()

	go func() {
		defer cancel()
		buf := make([]byte, 4096)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				if werr := c.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ == websocket.MessageBinary {
			if _, err := f.Write(data); err != nil && err != io.EOF {
				return
			}
			continue
		}
		var msg struct {
			Type       string `json:"type"`
			Cols, Rows uint16
		}
		if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" &&
			msg.Cols > 0 && msg.Cols < 500 && msg.Rows > 0 && msg.Rows < 200 {
			_ = pty.Setsize(f, &pty.Winsize{Cols: msg.Cols, Rows: msg.Rows})
		}
	}
}
