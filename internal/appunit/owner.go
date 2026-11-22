// SPDX-License-Identifier: AGPL-3.0-or-later

package appunit

import (
	"math"
	"os"
	"syscall"
)

// fileUID is the owner of a file; MaxUint32 when it is unknown, which
// matches no site user.
func fileUID(fi os.FileInfo) uint32 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Uid
	}

	return math.MaxUint32
}
