package logger

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLevelAppliesRegardlessOfOptionOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	l, err := New(WithFile(path), WithLevel("error"))
	if err != nil {
		t.Fatal(err)
	}
	l.Log.Info("must-not-appear")
	l.Log.Error("must-appear")
	if stopErr := l.Stop(context.Background()); stopErr != nil {
		t.Fatal(stopErr)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "must-not-appear") || !strings.Contains(string(data), "must-appear") {
		t.Fatalf("level filter failed: %s", data)
	}
}
