package config

import (
	"fmt"
	"strings"
	"time"
)

const (
	// Temporal Worker Controller sets these in the pod template of every
	// versioned Deployment it creates; they are not part of the file.
	EnvTemporalDeploymentName = "TEMPORAL_DEPLOYMENT_NAME"
	EnvTemporalWorkerBuildID  = "TEMPORAL_WORKER_BUILD_ID"

	DefaultTemporalHostPort       = "localhost:7233"
	DefaultTemporalNamespace      = "default"
	DefaultTemporalConnectTimeout = 10 * time.Second
)

// TemporalConfig is used by the worker process; the API process loads it but
// does not connect to Temporal. The task queue is fixed by ADR 5.
type TemporalConfig struct {
	HostPort       string        `yaml:"hostPort"`
	Namespace      string        `yaml:"namespace"`
	ConnectTimeout time.Duration `yaml:"connectTimeout"`
	// RegisterSearchAttributes allows the worker to create missing custom
	// Search Attributes; it needs operator permissions in the namespace.
	RegisterSearchAttributes bool `yaml:"registerSearchAttributes"`
	// DeploymentName and BuildID identify the Worker Deployment Version the
	// worker polls for; only the worker process needs them. Both come from the
	// env of Temporal Worker Controller.
	DeploymentName string `yaml:"-"`
	BuildID        string `yaml:"-"`
}

func (cfg TemporalConfig) Validate() error {
	if strings.TrimSpace(cfg.HostPort) == "" {
		return fmt.Errorf("spec.temporal.hostPort must not be empty")
	}
	if strings.TrimSpace(cfg.Namespace) == "" {
		return fmt.Errorf("spec.temporal.namespace must not be empty")
	}
	if cfg.ConnectTimeout <= 0 {
		return fmt.Errorf("spec.temporal.connectTimeout must be positive, got %s", cfg.ConnectTimeout)
	}
	return nil
}

// ValidateVersion checks the Worker Deployment Version of the worker process.
func (cfg TemporalConfig) ValidateVersion() error {
	if strings.TrimSpace(cfg.DeploymentName) == "" {
		return fmt.Errorf("%s must not be empty", EnvTemporalDeploymentName)
	}
	if strings.TrimSpace(cfg.BuildID) == "" {
		return fmt.Errorf("%s must not be empty", EnvTemporalWorkerBuildID)
	}
	return nil
}
