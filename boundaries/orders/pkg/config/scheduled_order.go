package config

import (
	"fmt"
	"time"
)

// The plan of a Scheduled Order has no defaults (FR-693): an empty,
// unparsable or non-positive value stops the process at startup.
const (
	KeyScheduledOrderPreparationBuffer = "spec.scheduledOrder.preparationBuffer"
	KeyScheduledOrderReplanLead        = "spec.scheduledOrder.replanLead"
)

// ScheduledOrderConfig holds the plan parameters of a Scheduled Order:
// start_at = planned_ready_at − preparation − PreparationBuffer and the replan
// checkpoint ReplanLead before start_at (FR-677).
type ScheduledOrderConfig struct {
	PreparationBuffer time.Duration `yaml:"preparationBuffer"`
	ReplanLead        time.Duration `yaml:"replanLead"`
}

func (cfg ScheduledOrderConfig) Validate() error {
	if cfg.PreparationBuffer <= 0 {
		return fmt.Errorf("%s must be positive, got %s", KeyScheduledOrderPreparationBuffer, cfg.PreparationBuffer)
	}
	if cfg.ReplanLead <= 0 {
		return fmt.Errorf("%s must be positive, got %s", KeyScheduledOrderReplanLead, cfg.ReplanLead)
	}
	return nil
}
