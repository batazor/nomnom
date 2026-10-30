// Package registry is the publishroutes.Registry on the Cell and Market
// resources (ADR 41). Reads go through the cache of the manager; the outcome is
// patched into the status subresource only when it changes.
package registry

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/batazor/nomnom/boundaries/controlplane/infrastructure/kubernetes/api/v1alpha1"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/publishroutes"
)

type Registry struct {
	client client.Client
}

var _ publishroutes.Registry = Registry{}

func New(c client.Client) Registry { return Registry{client: c} }

// Read returns every declared Cell and Market.
func (r Registry) Read(ctx context.Context) (publishroutes.Topology, error) {
	var cells v1alpha1.CellList
	if err := r.client.List(ctx, &cells); err != nil {
		return publishroutes.Topology{}, fmt.Errorf("list cells: %w", err)
	}
	var markets v1alpha1.MarketList
	if err := r.client.List(ctx, &markets); err != nil {
		return publishroutes.Topology{}, fmt.Errorf("list markets: %w", err)
	}

	topology := publishroutes.Topology{
		Cells:   make([]publishroutes.DeclaredCell, 0, len(cells.Items)),
		Markets: make([]publishroutes.DeclaredMarket, 0, len(markets.Items)),
	}
	for _, cell := range cells.Items {
		topology.Cells = append(topology.Cells, publishroutes.DeclaredCell{
			ID:              cell.Name,
			RouterNamespace: cell.Spec.Router.Namespace,
			RouterName:      cell.Spec.Router.Name,
			RouterPort:      cell.Spec.Router.Port,
		})
	}
	for _, market := range markets.Items {
		topology.Markets = append(topology.Markets, publishroutes.DeclaredMarket{
			ID:       market.Name,
			Name:     market.Spec.DisplayName,
			Currency: market.Spec.Currency,
			TimeZone: market.Spec.TimeZone,
			CellID:   market.Spec.CellRef,
			Archived: market.Spec.Archived,
		})
	}
	return topology, nil
}

// Record writes the Ready condition of every resource that has an outcome. Every
// resource is tried, and all failures are returned.
func (r Registry) Record(ctx context.Context, outcomes publishroutes.Outcomes) error {
	var cells v1alpha1.CellList
	if err := r.client.List(ctx, &cells); err != nil {
		return fmt.Errorf("list cells: %w", err)
	}
	var markets v1alpha1.MarketList
	if err := r.client.List(ctx, &markets); err != nil {
		return fmt.Errorf("list markets: %w", err)
	}

	var errs []error
	for i := range cells.Items {
		cell := &cells.Items[i]
		if outcome, found := outcomes.Cells[cell.Name]; found {
			errs = append(errs, r.record(ctx, cell, &cell.Status, outcome))
		}
	}
	for i := range markets.Items {
		market := &markets.Items[i]
		if outcome, found := outcomes.Markets[market.Name]; found {
			errs = append(errs, r.record(ctx, market, &market.Status, outcome))
		}
	}
	return errors.Join(errs...)
}

// record patches the status of object, whose status field is status.
func (r Registry) record(ctx context.Context, object client.Object, status *v1alpha1.RegistryStatus, outcome publishroutes.Outcome) error {
	original := object.DeepCopyObject().(client.Object)
	state := metav1.ConditionFalse
	if outcome.Ready {
		state = metav1.ConditionTrue
	}
	changed := meta.SetStatusCondition(&status.Conditions, metav1.Condition{
		Type:               v1alpha1.ConditionReady,
		Status:             state,
		ObservedGeneration: object.GetGeneration(),
		Reason:             string(outcome.Reason),
		Message:            outcome.Message,
	})
	if !changed && status.ObservedGeneration == object.GetGeneration() {
		return nil
	}
	status.ObservedGeneration = object.GetGeneration()
	if err := r.client.Status().Patch(ctx, object, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("patch status of %s: %w", object.GetName(), client.IgnoreNotFound(err))
	}
	return nil
}
