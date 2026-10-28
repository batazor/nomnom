package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

const (
	configKeyScheduledOrderPreparationBuffer = "scheduled_order.preparation_buffer"
	configKeyScheduledOrderReplanLead        = "scheduled_order.replan_lead"

	// The plan of a Scheduled Order has no defaults (FR-693): an empty,
	// unparsable or non-positive value stops the process at startup.
	EnvScheduledOrderPreparationBuffer = "SCHEDULED_ORDER__PREPARATION_BUFFER"
	EnvScheduledOrderReplanLead        = "SCHEDULED_ORDER__REPLAN_LEAD"
)

// ScheduledOrderConfig holds the plan parameters of a Scheduled Order:
// start_at = planned_ready_at − preparation − PreparationBuffer and the replan
// checkpoint ReplanLead before start_at (FR-677).
type ScheduledOrderConfig struct {
	PreparationBuffer time.Duration `mapstructure:"preparation_buffer"`
	ReplanLead        time.Duration `mapstructure:"replan_lead"`
}

func configureScheduledOrder(loader *viper.Viper) error {
	for key, env := range map[string]string{
		configKeyScheduledOrderPreparationBuffer: EnvScheduledOrderPreparationBuffer,
		configKeyScheduledOrderReplanLead:        EnvScheduledOrderReplanLead,
	} {
		if err := loader.BindEnv(key, env); err != nil {
			return fmt.Errorf("bind %s: %w", env, err)
		}
	}
	return nil
}

func (cfg ScheduledOrderConfig) Validate() error {
	if cfg.PreparationBuffer <= 0 {
		return fmt.Errorf("%s must be positive, got %s", EnvScheduledOrderPreparationBuffer, cfg.PreparationBuffer)
	}
	if cfg.ReplanLead <= 0 {
		return fmt.Errorf("%s must be positive, got %s", EnvScheduledOrderReplanLead, cfg.ReplanLead)
	}
	return nil
}
