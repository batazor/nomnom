package gatewayroutes_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/publishroutes"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/infrastructure/gatewayroutes"
)

var cfg = gatewayroutes.Config{Namespace: "nomnom-gateway", Gateway: "public", Listener: "api"}

func routes(t *testing.T) []publishroutes.MarketRoute {
	t.Helper()
	router, err := vo.NewRouter("nomnom-bff", "bff", 3010)
	require.NoError(t, err)
	return []publishroutes.MarketRoute{
		{Market: market.New("berlin", "Berlin 100%", "EUR", "Europe/Berlin", "cell-1"), Router: router},
		{Market: market.New("hamburg", "Hamburg", "EUR", "Europe/Berlin", "cell-1"), Router: router},
	}
}

func byName(t *testing.T, resources []gatewayroutes.Resource, kind, name string) *unstructured.Unstructured {
	t.Helper()
	for _, resource := range resources {
		if resource.Object.GetKind() == kind && resource.Object.GetName() == name {
			return resource.Object
		}
	}
	t.Fatalf("no %s %s", kind, name)
	return nil
}

func TestAMarketRouteSendsItsPathToTheRouterOfItsCell(t *testing.T) {
	resources, err := gatewayroutes.Build(cfg, routes(t))
	require.NoError(t, err)

	route := byName(t, resources, "HTTPRoute", "market-berlin")
	assert.Equal(t, "nomnom-gateway", route.GetNamespace())
	assert.Equal(t, map[string]string{
		"nomnom.dev/route": "market", "nomnom.dev/market": "berlin", "nomnom.dev/cell": "cell-1",
		"app.kubernetes.io/managed-by": "nomnom-controlplane",
	}, route.GetLabels())

	parents, _, _ := unstructured.NestedSlice(route.Object, "spec", "parentRefs")
	assert.Equal(t, []any{map[string]any{"name": "public", "sectionName": "api"}}, parents)
	rules, _, _ := unstructured.NestedSlice(route.Object, "spec", "rules")
	require.Len(t, rules, 1)
	rule := rules[0].(map[string]any)
	assert.Equal(t, []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/m/berlin"}}}, rule["matches"])
	assert.Equal(t, []any{
		map[string]any{"type": "URLRewrite", "urlRewrite": map[string]any{"path": map[string]any{"type": "ReplacePrefixMatch", "replacePrefixMatch": "/"}}},
		map[string]any{"type": "RequestHeaderModifier", "requestHeaderModifier": map[string]any{"set": []any{map[string]any{"name": "X-Nomnom-Market", "value": "berlin"}}}},
	}, rule["filters"])
	assert.Equal(t, []any{map[string]any{"name": "bff", "namespace": "nomnom-bff", "port": int64(3010)}}, rule["backendRefs"])
}

func TestTheMarketsListIsServedByTheGateway(t *testing.T) {
	resources, err := gatewayroutes.Build(cfg, routes(t))
	require.NoError(t, err)

	body, _, _ := unstructured.NestedString(byName(t, resources, "ConfigMap", "markets").Object, "data", "response.body")
	assert.Contains(t, body, `"Berlin 100%%"`, "a literal %% is escaped for Envoy")
	var list struct {
		Markets []map[string]string `json:"markets"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.ReplaceAll(body, "%%", "%")), &list))
	assert.Equal(t, []map[string]string{
		{"id": "berlin", "name": "Berlin 100%", "currency": "EUR", "timeZone": "Europe/Berlin"},
		{"id": "hamburg", "name": "Hamburg", "currency": "EUR", "timeZone": "Europe/Berlin"},
	}, list.Markets, "in the order of the snapshot")

	filter := byName(t, resources, "HTTPRouteFilter", "markets")
	ref, _, _ := unstructured.NestedMap(filter.Object, "spec", "directResponse", "body", "valueRef")
	assert.Equal(t, map[string]any{"group": "", "kind": "ConfigMap", "name": "markets"}, ref)

	route := byName(t, resources, "HTTPRoute", "markets")
	rules, _, _ := unstructured.NestedSlice(route.Object, "spec", "rules")
	require.Len(t, rules, 1)
	assert.NotContains(t, rules[0].(map[string]any), "backendRefs", "Envoy answers itself")
	cors, _, _ := unstructured.NestedSlice(filter.Object, "spec", "directResponse", "header", "set")
	assert.Equal(t, []any{
		map[string]any{"name": "Access-Control-Allow-Origin", "value": "*"},
		map[string]any{"name": "Access-Control-Allow-Methods", "value": "GET"},
		map[string]any{"name": "Access-Control-Allow-Headers", "value": "traceparent, tracestate"},
	}, cors, "a direct response gets no CORS policy of Envoy, so the headers are on the response")
}

func TestTheMarketsListCarriesTheTimeZone(t *testing.T) {
	router, err := vo.NewRouter("nomnom-bff", "bff", 3010)
	require.NoError(t, err)
	resources, err := gatewayroutes.Build(cfg, []publishroutes.MarketRoute{
		{Market: market.New("berlin", "Berlin", "EUR", "Europe/Berlin", "cell-1"), Router: router},
		{Market: market.New("hamburg", "Hamburg", "EUR", "Europe/Berlin", "cell-1"), Router: router},
	})
	require.NoError(t, err)

	body, _, _ := unstructured.NestedString(byName(t, resources, "ConfigMap", "markets").Object, "data", "response.body")
	assert.JSONEq(t, `{"markets":[{"id":"berlin","name":"Berlin","currency":"EUR","timeZone":"Europe/Berlin"},{"id":"hamburg","name":"Hamburg","currency":"EUR","timeZone":"Europe/Berlin"}]}`, body)
}

func TestAnEmptyRegistryPublishesAnEmptyList(t *testing.T) {
	resources, err := gatewayroutes.Build(cfg, nil)
	require.NoError(t, err)
	require.Len(t, resources, 3)
	body, _, _ := unstructured.NestedString(resources[0].Object.Object, "data", "response.body")
	assert.Equal(t, `{"markets":[]}`, body)
}

// applied records server-side apply patches; failing names a resource to refuse.
func applied(t *testing.T, failing string) (*dynamicfake.FakeDynamicClient, *[]string) {
	t.Helper()
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	var names []string
	client.PrependReactor("patch", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patch := action.(k8stesting.PatchAction)
		require.Equal(t, types.ApplyPatchType, patch.GetPatchType())
		require.Equal(t, "nomnom-gateway", patch.GetNamespace())
		names = append(names, patch.GetResource().Resource+"/"+patch.GetName())
		if patch.GetName() == failing {
			return true, nil, errors.New("forbidden")
		}
		obj := &unstructured.Unstructured{}
		require.NoError(t, obj.UnmarshalJSON(patch.GetPatch()))
		return true, obj, nil
	})
	return client, &names
}

func TestApplyAppliesEveryResourceServerSide(t *testing.T) {
	client, names := applied(t, "")
	require.NoError(t, gatewayroutes.New(client, cfg).Apply(context.Background(), routes(t)))
	assert.Equal(t, []string{
		"configmaps/markets", "httproutefilters/markets", "httproutes/markets",
		"httproutes/market-berlin", "httproutes/market-hamburg",
	}, *names)
}

func TestApplyReportsAFailureAndAppliesTheRest(t *testing.T) {
	client, names := applied(t, "market-berlin")
	err := gatewayroutes.New(client, cfg).Apply(context.Background(), routes(t))
	assert.ErrorContains(t, err, "httproutes nomnom-gateway/market-berlin")
	assert.Contains(t, *names, "httproutes/market-hamburg")
}
