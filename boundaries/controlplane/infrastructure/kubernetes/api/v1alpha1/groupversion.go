// Package v1alpha1 is the registry of the control plane (ADR 41): the Cell and
// Market resources, the only source of truth of the topology. Their schema and
// validation rules are the markers below; `make generate` writes the manifests
// into the chart.
//
// +kubebuilder:object:generate=true
// +groupName=controlplane.nomnom.dev
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is the API group of the registry.
	GroupVersion = schema.GroupVersion{Group: "controlplane.nomnom.dev", Version: "v1alpha1"}

	schemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds the resources of the registry to a scheme.
	AddToScheme = schemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion, &Cell{}, &CellList{}, &Market{}, &MarketList{})
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
