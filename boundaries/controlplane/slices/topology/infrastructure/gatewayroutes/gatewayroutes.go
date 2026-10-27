// Package gatewayroutes is the publishroutes.Applier on the Kubernetes API
// (ADR 41): it builds the resources of the public gateway from the registry
// snapshot and applies them server-side. Per Market an HTTPRoute sends
// /m/{market_id} to the Router of its Cell, strips the prefix and sets
// X-Nomnom-Market; the list of Markets is a ConfigMap that Envoy serves itself
// on GET /markets through an HTTPRouteFilter directResponse.
package gatewayroutes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/publishroutes"
)

const (
	// FieldManager owns the applied fields of every resource of the control plane.
	FieldManager = "nomnom-controlplane"
	// MarketHeader is the header the route sets from its Market.
	MarketHeader = "X-Nomnom-Market"
	// MarketsName names the ConfigMap, HTTPRouteFilter and HTTPRoute of the Markets list.
	MarketsName = "markets"
	// MarketsPath is the path of the Markets list.
	MarketsPath = "/markets"
	// bodyKey is the ConfigMap key Envoy Gateway reads the direct response from.
	bodyKey = "response.body"

	LabelRoute  = "nomnom.dev/route"
	LabelMarket = "nomnom.dev/market"
	LabelCell   = "nomnom.dev/cell"
	// RouteMarket is the LabelRoute of a Market route; the gateway policy of
	// Market routes selects them by it.
	RouteMarket = "market"
	routeList   = "markets"
)

var (
	gvrHTTPRoute       = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}
	gvrHTTPRouteFilter = schema.GroupVersionResource{Group: "gateway.envoyproxy.io", Version: "v1alpha1", Resource: "httproutefilters"}
	gvrConfigMap       = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
)

// Config is where the routes go: the namespace of the public gateway, the
// Gateway and its listener for client traffic.
type Config struct {
	Namespace string
	Gateway   string
	Listener  string
}

// Resource is one object to apply with its API resource.
type Resource struct {
	GVR    schema.GroupVersionResource
	Object *unstructured.Unstructured
}

// Applier applies the resources server-side as FieldManager.
type Applier struct {
	client dynamic.Interface
	cfg    Config
}

var _ publishroutes.Applier = Applier{}

func New(client dynamic.Interface, cfg Config) Applier { return Applier{client: client, cfg: cfg} }

// NewInCluster connects with the ServiceAccount of the pod: the control plane
// runs in the cluster of the public gateway.
func NewInCluster(cfg Config) (Applier, error) {
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return Applier{}, fmt.Errorf("kubernetes in-cluster config: %w", err)
	}
	client, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return Applier{}, fmt.Errorf("kubernetes client: %w", err)
	}
	return New(client, cfg), nil
}

// Apply applies the list of Markets first and the Market routes after it;
// every resource is applied even when one fails, and all failures are returned.
func (a Applier) Apply(ctx context.Context, routes []publishroutes.MarketRoute) error {
	resources, err := Build(a.cfg, routes)
	if err != nil {
		return err
	}
	var errs []error
	for _, resource := range resources {
		name := resource.Object.GetName()
		_, err := a.client.Resource(resource.GVR).Namespace(a.cfg.Namespace).
			Apply(ctx, name, resource.Object, metav1.ApplyOptions{FieldManager: FieldManager, Force: true})
		if err != nil {
			errs = append(errs, fmt.Errorf("apply %s %s/%s: %w", resource.GVR.Resource, a.cfg.Namespace, name, err))
		}
	}
	return errors.Join(errs...)
}

// Build returns the whole set of resources of the snapshot: the ConfigMap,
// HTTPRouteFilter and HTTPRoute of the Markets list, then an HTTPRoute per
// Market in the order of the snapshot.
func Build(cfg Config, routes []publishroutes.MarketRoute) ([]Resource, error) {
	body, err := marketsBody(routes)
	if err != nil {
		return nil, err
	}
	resources := []Resource{
		{GVR: gvrConfigMap, Object: object("v1", "ConfigMap", cfg.Namespace, MarketsName, routeList, map[string]any{
			"data": map[string]any{bodyKey: body},
		})},
		{GVR: gvrHTTPRouteFilter, Object: object("gateway.envoyproxy.io/v1alpha1", "HTTPRouteFilter", cfg.Namespace, MarketsName, routeList, map[string]any{
			"spec": map[string]any{"directResponse": map[string]any{
				"statusCode":  int64(200),
				"contentType": "application/json",
				"body": map[string]any{
					"type":     "ValueRef",
					"valueRef": map[string]any{"group": "", "kind": "ConfigMap", "name": MarketsName},
				},
				// CORS of a public list without credentials, as the Router allows any
				// origin. Envoy takes a CORS policy and route response headers from the
				// route entry, which a direct response has not, so the headers are part of
				// the response itself; Faro adds W3C trace context to every request.
				"header": map[string]any{"set": []any{
					map[string]any{"name": "Access-Control-Allow-Origin", "value": "*"},
					map[string]any{"name": "Access-Control-Allow-Methods", "value": "GET"},
					map[string]any{"name": "Access-Control-Allow-Headers", "value": "traceparent, tracestate"},
				}},
			}},
		})},
		{GVR: gvrHTTPRoute, Object: object("gateway.networking.k8s.io/v1", "HTTPRoute", cfg.Namespace, MarketsName, routeList, map[string]any{
			"spec": map[string]any{
				"parentRefs": parentRefs(cfg),
				"rules": []any{map[string]any{
					// The preflight OPTIONS gets the same answer with the CORS headers.
					"matches": []any{methodMatch("GET"), methodMatch("OPTIONS")},
					"filters": []any{map[string]any{
						"type":         "ExtensionRef",
						"extensionRef": map[string]any{"group": "gateway.envoyproxy.io", "kind": "HTTPRouteFilter", "name": MarketsName},
					}},
				}},
			},
		})},
	}
	for _, route := range routes {
		resources = append(resources, Resource{GVR: gvrHTTPRoute, Object: marketRoute(cfg, route)})
	}
	return resources, nil
}

// RouteName is the HTTPRoute of a Market.
func RouteName(marketID string) string { return "market-" + marketID }

// PathPrefix is the path of a Market; Gateway API matches a prefix on path
// segments, so /m/berlin does not match /m/berlinx.
func PathPrefix(marketID string) string { return "/m/" + marketID }

func marketRoute(cfg Config, route publishroutes.MarketRoute) *unstructured.Unstructured {
	marketID := string(route.Market.ID())
	obj := object("gateway.networking.k8s.io/v1", "HTTPRoute", cfg.Namespace, RouteName(marketID), RouteMarket, map[string]any{
		"spec": map[string]any{
			"parentRefs": parentRefs(cfg),
			"rules": []any{map[string]any{
				"matches": []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": PathPrefix(marketID)}}},
				"filters": []any{
					map[string]any{"type": "URLRewrite", "urlRewrite": map[string]any{
						"path": map[string]any{"type": "ReplacePrefixMatch", "replacePrefixMatch": "/"},
					}},
					// set overwrites a value sent by the client.
					map[string]any{"type": "RequestHeaderModifier", "requestHeaderModifier": map[string]any{
						"set": []any{map[string]any{"name": MarketHeader, "value": marketID}},
					}},
				},
				"backendRefs": []any{map[string]any{
					"name":      route.Router.Name(),
					"namespace": route.Router.Namespace(),
					"port":      int64(route.Router.Port()),
				}},
			}},
		},
	})
	labels := obj.GetLabels()
	labels[LabelMarket] = marketID
	labels[LabelCell] = string(route.Market.CellID())
	obj.SetLabels(labels)
	return obj
}

// marketsBody is the JSON list of Markets by the order of the snapshot. The
// body of a direct response is a format string of Envoy command operators,
// so a literal % is written as %%.
func marketsBody(routes []publishroutes.MarketRoute) (string, error) {
	type entry struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Currency string `json:"currency"`
	}
	list := struct {
		Markets []entry `json:"markets"`
	}{Markets: make([]entry, 0, len(routes))}
	for _, route := range routes {
		list.Markets = append(list.Markets, entry{
			ID: string(route.Market.ID()), Name: string(route.Market.Name()), Currency: string(route.Market.Currency()),
		})
	}
	encoded, err := json.Marshal(list)
	if err != nil {
		return "", fmt.Errorf("encode markets: %w", err)
	}
	return strings.ReplaceAll(string(encoded), "%", "%%"), nil
}

func parentRefs(cfg Config) []any {
	return []any{map[string]any{"name": cfg.Gateway, "sectionName": cfg.Listener}}
}

func methodMatch(method string) map[string]any {
	return map[string]any{"method": method, "path": map[string]any{"type": "Exact", "value": MarketsPath}}
}

func object(apiVersion, kind, namespace, name, route string, content map[string]any) *unstructured.Unstructured {
	content["apiVersion"] = apiVersion
	content["kind"] = kind
	content["metadata"] = map[string]any{
		"name":      name,
		"namespace": namespace,
		"labels":    map[string]any{LabelRoute: route, "app.kubernetes.io/managed-by": FieldManager},
	}
	return &unstructured.Unstructured{Object: content}
}
