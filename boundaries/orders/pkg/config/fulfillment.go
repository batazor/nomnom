package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const (
	configKeyFulfillment = "fulfillment"
	envPrefixFulfillment = "FULFILLMENT__"
)

// FulfillmentConfig holds the Policy of the Order Fulfillment Workflow. The
// defaults equal the DefaultPolicy of the fulfillment Temporal adapter and are
// placeholders, not product decisions.
type FulfillmentConfig struct {
	AcceptanceTimeout       time.Duration `mapstructure:"acceptance_timeout"`
	AssignmentTimeout       time.Duration `mapstructure:"assignment_timeout"`
	MaxReplans              int           `mapstructure:"max_replans"`
	OperatorDecisionTimeout time.Duration `mapstructure:"operator_decision_timeout"`
	PreparationTimeout      time.Duration `mapstructure:"preparation_timeout"`
	MaxPreparationExtension time.Duration `mapstructure:"max_preparation_extension"`
	PickupTimeout           time.Duration `mapstructure:"pickup_timeout"`
	DeliveryTimeout         time.Duration `mapstructure:"delivery_timeout"`
	PublishStage            bool          `mapstructure:"publish_stage"`
}

var fulfillmentDefaults = map[string]any{
	"acceptance_timeout":        10 * time.Minute,
	"assignment_timeout":        15 * time.Minute,
	"max_replans":               2,
	"operator_decision_timeout": 30 * time.Minute,
	"preparation_timeout":       90 * time.Minute,
	"max_preparation_extension": 60 * time.Minute,
	"pickup_timeout":            60 * time.Minute,
	"delivery_timeout":          120 * time.Minute,
	"publish_stage":             true,
}

// EnvFulfillment returns the environment variable of a fulfillment key, for
// example FULFILLMENT__ACCEPTANCE_TIMEOUT.
func EnvFulfillment(key string) string { return envPrefixFulfillment + strings.ToUpper(key) }

func configureFulfillment(loader *viper.Viper) error {
	for key, value := range fulfillmentDefaults {
		path := configKeyFulfillment + "." + key
		loader.SetDefault(path, value)
		if err := loader.BindEnv(path, EnvFulfillment(key)); err != nil {
			return fmt.Errorf("bind %s: %w", EnvFulfillment(key), err)
		}
	}
	return nil
}

func (cfg FulfillmentConfig) Validate() error {
	for name, value := range map[string]time.Duration{
		"acceptance_timeout":        cfg.AcceptanceTimeout,
		"assignment_timeout":        cfg.AssignmentTimeout,
		"operator_decision_timeout": cfg.OperatorDecisionTimeout,
		"preparation_timeout":       cfg.PreparationTimeout,
		"pickup_timeout":            cfg.PickupTimeout,
		"delivery_timeout":          cfg.DeliveryTimeout,
	} {
		if value <= 0 {
			return fmt.Errorf("fulfillment %s must be positive, got %s", name, value)
		}
	}
	if cfg.MaxPreparationExtension < 0 {
		return fmt.Errorf("fulfillment max_preparation_extension must not be negative, got %s", cfg.MaxPreparationExtension)
	}
	if cfg.MaxReplans < 0 {
		return fmt.Errorf("fulfillment max_replans must not be negative, got %d", cfg.MaxReplans)
	}
	return nil
}
