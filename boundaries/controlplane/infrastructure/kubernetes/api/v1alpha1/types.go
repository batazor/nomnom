package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConditionReady is the condition of a registry resource: the control plane
// accepted it and, for a Market, published its routes.
const ConditionReady = "Ready"

// RegistryStatus is the outcome of the last reconcile of a resource.
type RegistryStatus struct {
	// ObservedGeneration is the generation of the spec the status describes.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Conditions holds the Ready condition.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Router is the Kubernetes Service of the Cosmo Router of a Cell: the entry of
// the Cell for clients.
type Router struct {
	// Namespace of the Service.
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Namespace string `json:"namespace"`
	// Name of the Service.
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`
	// Port of the Service.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`
}

// CellSpec declares a Cell. It is immutable.
//
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="the spec of a Cell is immutable"
type CellSpec struct {
	// Router is the entry of the Cell for clients.
	Router Router `json:"router"`
}

// Cell is a complete isolated instance of the data plane. Its name is cell_id.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:metadata:annotations="helm.sh/resource-policy=keep"
// +kubebuilder:printcolumn:name="Router",type=string,JSONPath=`.spec.router.name`
// +kubebuilder:printcolumn:name="Namespace",type=string,JSONPath=`.spec.router.namespace`
// +kubebuilder:printcolumn:name="Port",type=integer,JSONPath=`.spec.router.port`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:validation:XValidation:rule="self.metadata.name.matches('^[a-z][a-z0-9-]{1,31}$')",message="the name of a Cell must match ^[a-z][a-z0-9-]{1,31}$"
type Cell struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec CellSpec `json:"spec"`
	// +optional
	Status RegistryStatus `json:"status,omitzero"`
}

// CellList is a list of Cells.
//
// +kubebuilder:object:root=true
type CellList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Cell `json:"items"`
}

// MarketSpec declares a Market. Only archived changes, and only to true.
type MarketSpec struct {
	// DisplayName is the name clients see.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:XValidation:rule="self == self.trim()",message="displayName must not have surrounding whitespace"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="displayName is immutable"
	DisplayName string `json:"displayName"`
	// Currency is an ISO 4217 code.
	// +kubebuilder:validation:Pattern=`^[A-Z]{3}$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="currency is immutable"
	Currency string `json:"currency"`
	// TimeZone is an IANA time zone; the control plane checks that it exists.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="timeZone is immutable"
	TimeZone string `json:"timeZone"`
	// CellRef is the Market Placement; the control plane checks that the Cell exists.
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9-]{1,31}$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="cellRef is immutable"
	CellRef string `json:"cellRef"`
	// Archived takes the Market off the client list and the public gateway. The
	// name stays taken: a Market is never deleted and never leaves the archive.
	// +kubebuilder:default=false
	// +kubebuilder:validation:XValidation:rule="!oldSelf || self",message="an archived Market does not leave the archive"
	Archived bool `json:"archived"`
}

// Market is a city served by one Cell. Its name is market_id.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:metadata:annotations="helm.sh/resource-policy=keep"
// +kubebuilder:printcolumn:name="Cell",type=string,JSONPath=`.spec.cellRef`
// +kubebuilder:printcolumn:name="Currency",type=string,JSONPath=`.spec.currency`
// +kubebuilder:printcolumn:name="Time Zone",type=string,JSONPath=`.spec.timeZone`
// +kubebuilder:printcolumn:name="Archived",type=boolean,JSONPath=`.spec.archived`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:validation:XValidation:rule="self.metadata.name.matches('^[a-z][a-z0-9-]{1,31}$')",message="the name of a Market must match ^[a-z][a-z0-9-]{1,31}$"
type Market struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec MarketSpec `json:"spec"`
	// +optional
	Status RegistryStatus `json:"status,omitzero"`
}

// MarketList is a list of Markets.
//
// +kubebuilder:object:root=true
type MarketList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Market `json:"items"`
}
