// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"strings"
	"testing"
	"time"
)

func TestCronApplyRequestValidate(t *testing.T) {
	ok := CronJob{ID: 1, Schedule: "*/5 * * * *", Command: "php wp-cron.php"}

	if err := (CronApplyRequest{Site: "shop", Jobs: []CronJob{ok, {ID: 2, Schedule: "@daily", Command: "true", TimeoutSec: 60, Paused: true}}}).Validate(); err != nil {
		t.Fatal(err)
	}

	if err := (CronApplyRequest{Site: "shop"}).Validate(); err != nil {
		t.Fatalf("empty list: %v", err)
	}

	zero, twenty, minus := 0, 20, -1
	if ok.Niceness() != DefaultCronNice || (CronJob{Nice: &zero}).Niceness() != 0 {
		t.Errorf("niceness %d", ok.Niceness())
	}

	if ok.Timeout() != time.Hour {
		t.Errorf("default timeout %s", ok.Timeout())
	}

	many := make([]CronJob, 51)
	for i := range many {
		many[i] = ok
		many[i].ID = int64(i + 1)
	}

	for name, r := range map[string]CronApplyRequest{
		"site":     {Site: "root", Jobs: []CronJob{ok}},
		"id":       {Site: "shop", Jobs: []CronJob{{Schedule: ok.Schedule, Command: ok.Command}}},
		"twice":    {Site: "shop", Jobs: []CronJob{ok, ok}},
		"schedule": {Site: "shop", Jobs: []CronJob{{ID: 1, Schedule: "@reboot", Command: "x"}}},
		"command":  {Site: "shop", Jobs: []CronJob{{ID: 1, Schedule: "@daily", Command: "a\nb"}}},
		"short":    {Site: "shop", Jobs: []CronJob{{ID: 1, Schedule: "@daily", Command: "x", TimeoutSec: 59}}},
		"long":     {Site: "shop", Jobs: []CronJob{{ID: 1, Schedule: "@daily", Command: "x", TimeoutSec: 86401}}},
		"negative": {Site: "shop", Jobs: []CronJob{{ID: 1, Schedule: "@daily", Command: "x", TimeoutSec: -1}}},
		"huge":     {Site: "shop", Jobs: []CronJob{{ID: 1, Schedule: "@daily", Command: "x", TimeoutSec: 1 << 62}}},
		"too many": {Site: "shop", Jobs: many},
		"nice 20":  {Site: "shop", Jobs: []CronJob{{ID: 1, Schedule: "@daily", Command: "x", Nice: &twenty}}},
		"nice -1":  {Site: "shop", Jobs: []CronJob{{ID: 1, Schedule: "@daily", Command: "x", Nice: &minus}}},
		"big":      {Site: "shop", Jobs: []CronJob{{ID: 1, Schedule: "@daily", Command: strings.Repeat("x", 4097)}}},
	} {
		if r.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
