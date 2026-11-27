// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

func TestSiteHomeCopyOps(t *testing.T) {
	c, _ := startSiteAgent(t)
	ctx := context.Background()

	_, err := agentapi.SiteHomeCopy.Call(ctx, c, agentapi.SiteHomeCopyRequest{Source: "blog", Target: "blog"})
	wantCode(t, err, agentrpc.CodeInvalidArgument)

	_, err = agentapi.SiteHomeCopy.Call(ctx, c, agentapi.SiteHomeCopyRequest{Source: "blog", Target: "../x"})
	wantCode(t, err, agentrpc.CodeInvalidArgument)

	_, err = agentapi.SiteHomeCopy.Call(ctx, c, agentapi.SiteHomeCopyRequest{Source: "blog", Target: "stage"})
	wantCode(t, err, agentrpc.CodeNotFound)

	_, err = agentapi.SiteHomeCopyStatus.Call(ctx, c, agentapi.SiteHomeCopyStatusRequest{ID: "0123456789abcdef"})
	wantCode(t, err, agentrpc.CodeNotFound)

	_, err = agentapi.SiteHomeCopyStatus.Call(ctx, c, agentapi.SiteHomeCopyStatusRequest{ID: "../x"})
	wantCode(t, err, agentrpc.CodeInvalidArgument)
}
