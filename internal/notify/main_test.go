// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"os"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/mail"
)

func TestMain(m *testing.M) {
	mail.ServerZone = func() *time.Location { return time.UTC }

	os.Exit(m.Run())
}
