// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import "context"

// Health is the state of nginx for hostashi doctor.
type Health struct {
	Current   int      // generation the current link points to, 0: none
	Confirmed bool     // the current generation passed Verify
	Live      int      // generation nginx serves, 0 with LiveErr set
	LiveErr   error    // nginx did not answer on the control socket
	Drift     []string // generated files changed or removed by hand
}

// Health reads the state without changing anything. It takes the lock, so
// it waits for a running Apply instead of reading half a generation.
func (r *Reconciler) Health(ctx context.Context) (Health, error) {
	unlock, err := r.lock()
	if err != nil {
		return Health{}, err
	}

	defer unlock()

	var h Health

	cur, ok := r.current()
	if ok {
		h.Current, h.Confirmed, h.Drift = cur, r.confirmed(cur), r.drift(cur)
	}

	h.Live, h.LiveErr = r.Driver.liveGeneration(ctx)

	return h, nil
}
