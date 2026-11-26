package config

import (
	"strings"
	"testing"
	"time"
)

var workerEnvKeys = []string{
	"WORKER_ID", "WORKER_MAX_CONCURRENT_JOBS", "WORKER_POLL_INTERVAL", "WORKER_LEASE_TTL",
	"WORKER_HEARTBEAT_INTERVAL", "WORKER_RECOVERY_INTERVAL", "WORKER_REPROBE_INTERVAL",
	"WORKER_PROBE_TIMEOUT", "WORKER_EXEC_TIMEOUT", "WORKER_SHUTDOWN_TIMEOUT",
}

func workerBaseEnv(t *testing.T) {
	t.Helper()
	baseEnv(t)
	for _, k := range workerEnvKeys {
		t.Setenv(k, "")
	}
}

func TestLoadWorkerDefaults(t *testing.T) {
	workerBaseEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := WorkerConfig{
		MaxConcurrentJobs: 4,
		PollInterval:      2 * time.Second,
		LeaseTTL:          15 * time.Second,
		HeartbeatInterval: 5 * time.Second,
		RecoveryInterval:  5 * time.Second,
		ReprobeInterval:   15 * time.Second,
		ProbeTimeout:      10 * time.Second,
		ExecTimeout:       30 * time.Minute,
		ShutdownTimeout:   30 * time.Second,
	}
	if cfg.Worker != want {
		t.Fatalf("Worker = %+v; want %+v", cfg.Worker, want)
	}
}

func TestLoadWorkerOverrides(t *testing.T) {
	workerBaseEnv(t)
	t.Setenv("WORKER_ID", "w-1")
	t.Setenv("WORKER_MAX_CONCURRENT_JOBS", "8")
	t.Setenv("WORKER_LEASE_TTL", "30s")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Worker.ID != "w-1" || cfg.Worker.MaxConcurrentJobs != 8 || cfg.Worker.LeaseTTL != 30*time.Second {
		t.Fatalf("Worker = %+v", cfg.Worker)
	}
}

func TestLoadWorkerRejectsInvalidValues(t *testing.T) {
	cases := map[string][2]string{
		"zero jobs":           {"WORKER_MAX_CONCURRENT_JOBS", "0"},
		"bad duration":        {"WORKER_POLL_INTERVAL", "soon"},
		"heartbeat above ttl": {"WORKER_HEARTBEAT_INTERVAL", "20s"},
		"negative exec limit": {"WORKER_EXEC_TIMEOUT", "-1m"},
	}
	for name, kv := range cases {
		t.Run(name, func(t *testing.T) {
			workerBaseEnv(t)
			t.Setenv(kv[0], kv[1])
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), "WORKER_") {
				t.Fatalf("err = %v; want a WORKER_ error", err)
			}
		})
	}
}
