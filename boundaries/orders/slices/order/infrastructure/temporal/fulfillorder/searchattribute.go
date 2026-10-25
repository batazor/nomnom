package fulfillorder

import (
	"context"
	"errors"
	"fmt"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/api/serviceerror"
)

// ErrSearchAttributeMissing means StageSearchAttribute is not registered and
// the worker is not allowed to register it.
var ErrSearchAttributeMissing = errors.New("search attribute is not registered")

// EnsureStageSearchAttribute makes sure the namespace has the Keyword Search
// Attribute published by the workflow; with register it creates a missing one.
func EnsureStageSearchAttribute(ctx context.Context, operator operatorservice.OperatorServiceClient, namespace string, register bool) error {
	name := StageSearchAttribute.GetName()
	existing, err := operator.ListSearchAttributes(ctx, &operatorservice.ListSearchAttributesRequest{Namespace: namespace})
	if err != nil {
		return fmt.Errorf("list search attributes of namespace %s: %w", namespace, err)
	}
	switch kind, ok := existing.GetCustomAttributes()[name]; {
	case ok && kind == enumspb.INDEXED_VALUE_TYPE_KEYWORD:
		return nil
	case ok:
		return fmt.Errorf("search attribute %s in namespace %s has type %s, want Keyword", name, namespace, kind)
	case !register:
		return fmt.Errorf("%w: %s in namespace %s; create it with `temporal operator search-attribute create --namespace %s --name %s --type Keyword`, set TEMPORAL__REGISTER_SEARCH_ATTRIBUTES=true or FULFILLMENT__PUBLISH_STAGE=false",
			ErrSearchAttributeMissing, name, namespace, namespace, name)
	}

	_, err = operator.AddSearchAttributes(ctx, &operatorservice.AddSearchAttributesRequest{
		Namespace:        namespace,
		SearchAttributes: map[string]enumspb.IndexedValueType{name: enumspb.INDEXED_VALUE_TYPE_KEYWORD},
	})
	var exists *serviceerror.AlreadyExists
	if err != nil && !errors.As(err, &exists) {
		return fmt.Errorf("register search attribute %s in namespace %s: %w", name, namespace, err)
	}
	return nil
}
