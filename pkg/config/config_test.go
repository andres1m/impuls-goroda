package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadStrictEnvironment(t *testing.T) {
	t.Setenv("TEST_DB_URL", "postgres://user:password@localhost/db")
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte("database:\n  host: ${TEST_DB_URL}\n  connection-timeout: 3s\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var c struct {
		Database Database `yaml:"database"`
	}
	if err := Load(p, &c); err != nil {
		t.Fatal(err)
	}
	if c.Database.Host != os.Getenv("TEST_DB_URL") || c.Database.ConnectionTimeout != 3*time.Second {
		t.Fatal("incorrect config")
	}
	for _, input := range []string{"unknown: secret-value", "database: [secret-value]", "database:\n  host: ${MISSING_TEST_ENV_VAR}", "database: {}\n---\ndatabase: {}"} {
		if err := os.WriteFile(p, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		err := Load(p, &c)
		if err == nil {
			t.Fatalf("accepted invalid config: %s", input)
		}
		if strings.Contains(err.Error(), "secret-value") {
			t.Fatal("secret leaked")
		}
	}
}

func TestRepositoryExample(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:password@localhost/test")
	t.Setenv("REDIS_PASSWORD", "example")
	var cfg struct {
		Logger   Logger     `yaml:"logger"`
		Database Database   `yaml:"database"`
		Redis    Redis      `yaml:"redis"`
		Temporal Temporal   `yaml:"temporal"`
		Server   GRPCServer `yaml:"grpc-server"`
		Client   GRPCClient `yaml:"grpc-client"`
	}
	if err := Load("../../config.example.yaml", &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Redis.Mode != "cluster" {
		t.Fatal("example must use cluster")
	}
}
