// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"errors"
	"testing"
)

func TestCheckCacheSize(t *testing.T) {
	const gib = int64(1) << 30

	for _, c := range []struct {
		mb    int
		limit int64
		ok    bool
	}{
		{64, 0, true},
		{1024, 0, true},
		{256, 512 << 20, true},  // exactly half
		{257, 512 << 20, false}, // more than half of the site's memory
		{512, gib, true},
		{0, 0, false},
		{31, 0, false},
		{1025, 0, false},
	} {
		err := checkCacheSize(c.mb, c.limit)
		if (err == nil) != c.ok || (err != nil && !errors.Is(err, ErrInvalid)) {
			t.Errorf("checkCacheSize(%d, %d) = %v", c.mb, c.limit, err)
		}
	}
}
