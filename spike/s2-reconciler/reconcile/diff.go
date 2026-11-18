package reconcile

import (
	"sort"
	"strings"
)

// diffFiles renders a small line diff between two file sets (diff preview).
func diffFiles(old, new map[string][]byte) string {
	names := map[string]bool{}
	for n := range old {
		names[n] = true
	}
	for n := range new {
		names[n] = true
	}
	var sorted []string
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	var b strings.Builder
	for _, n := range sorted {
		o, ook := old[n]
		w, wok := new[n]
		if ook && wok && string(o) == string(w) {
			continue
		}
		switch {
		case !ook:
			b.WriteString("+++ " + n + " (new)\n")
		case !wok:
			b.WriteString("--- " + n + " (removed)\n")
		default:
			b.WriteString("~~~ " + n + "\n")
		}
		for _, l := range lineDiff(lines(o), lines(w)) {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

func lines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// lineDiff is a plain LCS diff, fine for config-sized files.
func lineDiff(a, b []string) []string {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, "- "+a[i])
			i++
		default:
			out = append(out, "+ "+b[j])
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, "- "+a[i])
	}
	for ; j < m; j++ {
		out = append(out, "+ "+b[j])
	}
	return out
}
