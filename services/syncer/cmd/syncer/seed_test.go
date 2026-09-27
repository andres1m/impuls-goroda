package main

import "testing"

func TestParseSeedArgs(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"--from", "2026-10-01"}, "2026-10-01"},
		{[]string{"--from=2026-10-02"}, "2026-10-02"},
	} {
		got, err := parseSeedArgs(c.args)
		if err != nil || got != c.want {
			t.Fatalf("args %q: %q, %v", c.args, got, err)
		}
	}
	for _, args := range [][]string{{"--from", "01.10.2026"}, {"perm"}, {"--days", "3"}, {"--from"}} {
		if _, err := parseSeedArgs(args); err == nil {
			t.Fatalf("args %q accepted", args)
		}
	}
}
