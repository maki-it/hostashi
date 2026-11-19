// SPDX-License-Identifier: AGPL-3.0-or-later

package agentrpc

import (
	"context"
	"testing"

	"github.com/maki-it/hostashi/internal/reqid"
)

func TestRequestIDReachesTheHandler(t *testing.T) {
	sock, c := start(t, allowSelf(), Options{})

	id := reqid.New()

	r, err := opReqID.Call(reqid.With(context.Background(), id), c, emptyReq{})
	if err != nil || r.Text != id {
		t.Fatalf("%+v %v", r, err)
	}

	// Without an ID the field is left out and the handler sees none.
	r, err = opReqID.Call(context.Background(), c, emptyReq{})
	if err != nil || r.Text != "" {
		t.Fatalf("%+v %v", r, err)
	}

	// The agent checks the form itself; it does not trust the client.
	for _, bad := range []string{`"X"`, `"` + id + `\n"`, `"` + id[:31] + `G"`, `1`} {
		resp := rawCall(t, sock, []byte(`{"op":"reqid","params":{},"request_id":`+bad+`}`))
		if resp.OK || resp.Error == nil || resp.Error.Code != CodeBadRequest {
			t.Errorf("%s: %+v", bad, resp)
		}
	}
}
