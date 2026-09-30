package main

import "testing"

func TestParseCoverageArgs(t *testing.T) {
	for args, want := range map[string]string{"": "", "--city=perm": "perm", "--city=moscow": "moscow"} {
		var in []string
		if args != "" {
			in = []string{args}
		}
		got, err := parseCoverageArgs(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v, want %q", args, got, err, want)
		}
	}
	for _, args := range [][]string{{"--city=spb"}, {"perm"}, {"--unknown"}} {
		if _, err := parseCoverageArgs(args); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}
