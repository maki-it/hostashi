package main

import (
	"fmt"
	"math"
	"net/http"
	"time"
)

// metricsStream sends one synthetic sample per second as Server-Sent Events.
// The interval can be shortened with ?ms= for tests (min 50 ms).
func metricsStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	interval := time.Second
	if ms := r.URL.Query().Get("ms"); ms != "" {
		var n int
		if _, err := fmt.Sscanf(ms, "%d", &n); err == nil && n >= 50 && n <= 5000 {
			interval = time.Duration(n) * time.Millisecond
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	tk := time.NewTicker(interval)
	defer tk.Stop()
	i := 0
	for {
		select {
		case <-r.Context().Done():
			return
		case now := <-tk.C:
			i++
			x := float64(i) / 8
			cpu := 35 + 25*math.Sin(x) + 8*math.Sin(x*3.1)
			mem := 52 + 6*math.Sin(x/5)
			fmt.Fprintf(w, "event: sample\ndata: {\"t\":%d,\"cpu\":%.1f,\"mem\":%.1f}\n\n", now.Unix(), cpu, mem)
			fl.Flush()
		}
	}
}
