// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCloudPanelReports(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	id1, id2 := strings.Repeat("a", 32), strings.Repeat("b", 32)

	if err := s.CreateCloudPanelReport(ctx, CloudPanelReport{ID: id1, Sites: 21, Sealed: []byte("x"), CreatedAt: time.Unix(100, 0)}); err != nil {
		t.Fatal(err)
	}

	if err := s.CreateCloudPanelReport(ctx, CloudPanelReport{ID: id2, Sites: 2, Sealed: []byte("y"), CreatedAt: time.Unix(200, 0)}); err != nil {
		t.Fatal(err)
	}

	if err := s.CreateCloudPanelReport(ctx, CloudPanelReport{ID: id1, Sealed: []byte("z"), CreatedAt: time.Unix(300, 0)}); err == nil {
		t.Fatal("duplicate id accepted")
	}

	if err := s.CreateCloudPanelReport(ctx, CloudPanelReport{ID: "short", Sealed: []byte("z"), CreatedAt: time.Unix(300, 0)}); err == nil {
		t.Fatal("short id accepted")
	}

	list, err := s.CloudPanelReports(ctx)
	if err != nil || len(list) != 2 || list[0].ID != id2 || list[0].Sealed != nil || list[1].Sites != 21 {
		t.Fatalf("%+v %v", list, err)
	}

	r, err := s.CloudPanelReportByID(ctx, id1)
	if err != nil || string(r.Sealed) != "x" || !r.CreatedAt.Equal(time.Unix(100, 0)) {
		t.Fatalf("%+v %v", r, err)
	}

	if err := s.DeleteCloudPanelReport(ctx, id1); err != nil {
		t.Fatal(err)
	}

	if _, err := s.CloudPanelReportByID(ctx, id1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}

	if err := s.DeleteCloudPanelReport(ctx, id1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}
