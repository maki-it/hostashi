// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"testing"
	"time"
)

func TestDBRemoteJobs(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()

	srv, err := s.CreateDBServer(ctx, newServer("db1"), time.Unix(1_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}

	if got, err := s.DBServerByID(ctx, srv.ID); err != nil || got.Name != "db1" {
		t.Fatalf("DBServerByID = %+v, %v", got, err)
	}

	if _, err := s.DBServerByID(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing server: %v", err)
	}

	j := DBRemoteJob{JobID: "0123456789abcdef", Database: "blog_db", ServerID: srv.ID, User: "hostashi_job_0123456789abcdef", CreatedAt: time.Unix(2_000_000, 0).UTC()}
	if err := s.AddDBRemoteJob(ctx, j); err != nil {
		t.Fatal(err)
	}

	list, err := s.DBRemoteJobs(ctx)
	if err != nil || len(list) != 1 || list[0] != j {
		t.Fatalf("jobs = %+v, %v", list, err)
	}

	if err := s.AddDBRemoteJob(ctx, j); err == nil {
		t.Error("duplicate job accepted")
	}

	if err := s.DeleteDBRemoteJob(ctx, j.JobID); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteDBRemoteJob(ctx, j.JobID); err != nil {
		t.Errorf("second delete: %v", err)
	}

	if list, _ := s.DBRemoteJobs(ctx); len(list) != 0 {
		t.Errorf("jobs after delete = %+v", list)
	}
}

func TestDBRemoteTools(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()

	srv, err := s.CreateDBServer(ctx, newServer("db1"), time.Unix(1_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}

	late := DBRemoteTool{User: "hostashi_tool_0000000000000002", Database: "blog_db", ServerID: srv.ID, Expires: time.Unix(3_000_000, 0).UTC()}
	early := DBRemoteTool{User: "hostashi_tool_0000000000000001", Database: "blog_db", ServerID: srv.ID, Expires: time.Unix(2_000_000, 0).UTC()}

	for _, tl := range []DBRemoteTool{late, early} {
		if err := s.AddDBRemoteTool(ctx, tl); err != nil {
			t.Fatal(err)
		}
	}

	list, err := s.DBRemoteTools(ctx)
	if err != nil || len(list) != 2 || list[0] != early || list[1] != late {
		t.Fatalf("tools = %+v, %v", list, err)
	}

	if err := s.AddDBRemoteTool(ctx, early); err == nil {
		t.Error("duplicate user accepted")
	}

	if err := s.DeleteDBRemoteTool(ctx, early.User); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteDBRemoteTool(ctx, early.User); err != nil {
		t.Errorf("second delete: %v", err)
	}

	if list, _ := s.DBRemoteTools(ctx); len(list) != 1 || list[0] != late {
		t.Errorf("tools after delete = %+v", list)
	}
}
