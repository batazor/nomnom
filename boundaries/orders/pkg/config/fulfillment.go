package config

import (
	"fmt"
	"time"
)

// FulfillmentConfig holds the Policy of the Order Fulfillment Workflow. The
// defaults equal the DefaultPolicy of the fulfillment Temporal adapter and are
// placeholders, not product decisions.
type FulfillmentConfig struct {
	AcceptanceTimeout       time.Duration `yaml:"acceptanceTimeout"`
	PreparationTimeout      time.Duration `yaml:"preparationTimeout"`
	MaxPreparationExtension time.Duration `yaml:"maxPreparationExtension"`
	PickupTimeout           time.Duration `yaml:"pickupTimeout"`
	DeliveryTimeout         time.Duration `yaml:"deliveryTimeout"`
	PublishStage            bool          `yaml:"publishStage"`
}

// DefaultFulfillment — значения spec.fulfillment по умолчанию.
var DefaultFulfillment = FulfillmentConfig{
	AcceptanceTimeout:       10 * time.Minute,
	PreparationTimeout:      90 * time.Minute,
	MaxPreparationExtension: 60 * time.Minute,
	PickupTimeout:           60 * time.Minute,
	DeliveryTimeout:         120 * time.Minute,
	PublishStage:            true,
}

func (cfg FulfillmentConfig) Validate() error {
	for name, value := range map[string]time.Duration{
		"acceptanceTimeout":  cfg.AcceptanceTimeout,
		"preparationTimeout": cfg.PreparationTimeout,
		"pickupTimeout":      cfg.PickupTimeout,
		"deliveryTimeout":    cfg.DeliveryTimeout,
	} {
		if value <= 0 {
			return fmt.Errorf("spec.fulfillment.%s must be positive, got %s", name, value)
		}
	}
	if cfg.MaxPreparationExtension < 0 {
		return fmt.Errorf("spec.fulfillment.maxPreparationExtension must not be negative, got %s", cfg.MaxPreparationExtension)
	}
	return nil
}
