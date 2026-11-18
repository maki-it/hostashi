package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"sync"
)

type site struct {
	Domain  string `json:"domain"`
	User    string `json:"user"`
	Kind    string `json:"kind"`
	PHP     string `json:"php,omitempty"`
	DiskMB  int    `json:"diskMb"`
	QuotaMB int    `json:"quotaMb"`
}

type sites struct {
	mu    sync.Mutex
	list_ []site
}

func newSites() *sites {
	s := &sites{}
	kinds := []string{"php", "static", "node", "proxy"}
	for i := 0; i < 22; i++ { // 22 sites: Kim's current server
		k := kinds[i%len(kinds)]
		st := site{
			Domain:  fmt.Sprintf("kunde%02d.example.com", i+1),
			User:    fmt.Sprintf("kunde%02d", i+1),
			Kind:    k,
			DiskMB:  200 + (i*379)%4000,
			QuotaMB: 5000,
		}
		if k == "php" {
			st.PHP = []string{"8.2", "8.3", "8.4"}[i%3]
		}
		s.list_ = append(s.list_, st)
	}
	return s
}

func (s *sites) list(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.list_)
}

var (
	domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	userRe   = regexp.MustCompile(`^[a-z][a-z0-9_-]{2,31}$`)
)

type fieldErrors map[string]string

func (s *sites) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var in site
	if err := dec.Decode(&in); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	errs := fieldErrors{}
	if !domainRe.MatchString(in.Domain) || len(in.Domain) > 253 {
		errs["domain"] = "invalid domain"
	}
	if !userRe.MatchString(in.User) {
		errs["user"] = "invalid user name"
	}
	switch in.Kind {
	case "php", "static", "node", "proxy":
	default:
		errs["kind"] = "unknown site type"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.list_ {
		if e.Domain == in.Domain {
			errs["domain"] = "domain already exists"
		}
		if e.User == in.User {
			errs["user"] = "user already exists"
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if len(errs) > 0 {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{"errors": errs})
		return
	}
	in.QuotaMB, in.DiskMB = 5000, 0
	s.list_ = append(s.list_, in)
	sort.Slice(s.list_, func(i, j int) bool { return s.list_[i].Domain < s.list_[j].Domain })
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(in)
}
