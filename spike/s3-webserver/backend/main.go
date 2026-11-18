// Command backend is the stand-in for a Node or Python app behind the proxy.
package main

import (
	"bufio"
	"fmt"
	"net/http"
	"strings"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "proxy ok host=%s xff=%q\n", r.Host, r.Header.Get("X-Forwarded-For"))
	})
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "no upgrade", 400)
			return
		}
		c, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer c.Close()
		fmt.Fprint(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\nhello-from-backend\n")
		rw.Flush()
		line, _ := bufio.NewReader(c).ReadString('\n')
		fmt.Fprintf(c, "echo:%s", line)
	})
	_ = http.ListenAndServe("127.0.0.1:19100", nil)
}
