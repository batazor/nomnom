package controller_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/batazor/nomnom/boundaries/controlplane/infrastructure/kubernetes/api/v1alpha1"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/testfixture"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/publishroutes"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/infrastructure/controller"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/infrastructure/gatewayroutes"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/infrastructure/registry"
)

const (
	gatewayNamespace = "nomnom-gateway"
	eventually       = 90 * time.Second
	tick             = 200 * time.Millisecond
	chart            = "../../../../ops/helm/templates"
)

var kube client.Client

// TestMain runs the controller against a real API server: the CEL rules of the
// CRDs and the admission policy exist nowhere else. A Cell or Market is never
// deleted, so every test declares its own names.
func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctrl.SetLogger(logr.Discard())
	cluster := testfixture.MustStartK3s()
	defer cluster.Close()

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{v1alpha1.AddToScheme, corev1.AddToScheme} {
		if err := add(scheme); err != nil {
			log.Fatal(err)
		}
	}
	var err error
	if kube, err = client.New(cluster.Config, client.Options{Scheme: scheme}); err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := install(ctx); err != nil {
		log.Fatal(err)
	}

	mgr, err := controller.NewManager(cluster.Config, gatewayNamespace, 5*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	applier, err := gatewayroutes.NewForConfig(cluster.Config, gatewayroutes.Config{Namespace: gatewayNamespace, Gateway: "public", Listener: "api"})
	if err != nil {
		log.Fatal(err)
	}
	published, err := controller.Register(mgr, publishroutes.New(registry.New(mgr.GetClient()), applier))
	if err != nil {
		log.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- mgr.Start(ctx) }()
	select {
	case <-published:
	case err := <-stopped:
		log.Fatalf("manager stopped before the first publication: %v", err)
	case <-time.After(2 * time.Minute):
		log.Fatal("no first publication")
	}

	code := m.Run()
	cancel()
	if err := <-stopped; err != nil {
		log.Printf("manager stopped: %v", err)
	}
	return code
}

// install applies the CRDs and the policy of the chart, the stubs of the Gateway
// API resources, and the namespace of the public gateway.
func install(ctx context.Context) error {
	files, err := filepath.Glob(filepath.Join(chart, "crds", "*.yaml"))
	if err != nil {
		return fmt.Errorf("find crds: %w", err)
	}
	files = append(files, "testdata/gateway-crds.yaml", filepath.Join(chart, "registry-protection.yaml"))
	for _, file := range files {
		if err := applyFile(ctx, file); err != nil {
			return err
		}
	}
	if err := kube.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: gatewayNamespace}}); err != nil {
		return fmt.Errorf("create namespace: %w", err)
	}
	// A new CRD is served and a new policy enforced shortly after their creation.
	deadline := time.Now().Add(eventually)
	for {
		err := probe(ctx)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("registry is not ready: %w", err)
		}
		time.Sleep(tick)
	}
}

// probe passes when the API serves the registry and its stubs and the policy is
// enforced on a real resource.
func probe(ctx context.Context) error {
	if err := kube.List(ctx, &v1alpha1.MarketList{}); err != nil {
		return err
	}
	for _, kind := range []string{"HTTPRouteList", "HTTPRouteFilterList"} {
		list := &unstructured.UnstructuredList{}
		gvk := gatewayroutes.GVKHTTPRoute
		if kind == "HTTPRouteFilterList" {
			gvk = gatewayroutes.GVKHTTPRouteFilter
		}
		list.SetAPIVersion(gvk.GroupVersion().String())
		list.SetKind(kind)
		if err := kube.List(ctx, list); err != nil {
			return err
		}
	}
	cell := &v1alpha1.Cell{
		ObjectMeta: metav1.ObjectMeta{Name: "policy-probe"},
		Spec:       v1alpha1.CellSpec{Router: v1alpha1.Router{Namespace: "nomnom-bff", Name: "bff", Port: 3010}},
	}
	if err := kube.Create(ctx, cell); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	if err := kube.Delete(ctx, cell, client.DryRunAll); err == nil {
		return errors.New("the admission policy does not refuse deletion yet")
	}
	return nil
}

func applyFile(ctx context.Context, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	decoder := utilyaml.NewYAMLOrJSONDecoder(file, 4096)
	for {
		object := &unstructured.Unstructured{}
		if err := decoder.Decode(object); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("decode %s: %w", path, err)
		}
		if len(object.Object) == 0 {
			continue
		}
		if err := kube.Create(ctx, object); err != nil {
			return fmt.Errorf("create %s from %s: %w", object.GetName(), path, err)
		}
	}
}

func newCell(name string) *v1alpha1.Cell {
	return &v1alpha1.Cell{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.CellSpec{Router: v1alpha1.Router{Namespace: "nomnom-bff", Name: "bff", Port: 3010}},
	}
}

func newMarket(name, cell string) *v1alpha1.Market {
	return &v1alpha1.Market{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       v1alpha1.MarketSpec{DisplayName: "Market " + name, Currency: "EUR", TimeZone: "Europe/Berlin", CellRef: cell},
	}
}

func readyReason(t *testing.T, name string) string {
	t.Helper()
	var market v1alpha1.Market
	if err := kube.Get(t.Context(), client.ObjectKey{Name: name}, &market); err != nil {
		return ""
	}
	condition := meta.FindStatusCondition(market.Status.Conditions, v1alpha1.ConditionReady)
	if condition == nil {
		return ""
	}
	return condition.Reason
}

func route(t *testing.T, market string) error {
	t.Helper()
	object := &unstructured.Unstructured{}
	object.SetGroupVersionKind(gatewayroutes.GVKHTTPRoute)
	return kube.Get(t.Context(), client.ObjectKey{Namespace: gatewayNamespace, Name: gatewayroutes.RouteName(market)}, object)
}

// listed returns the ids of the list of Markets on the gateway.
func listed(t *testing.T) []string {
	t.Helper()
	var config corev1.ConfigMap
	if err := kube.Get(t.Context(), client.ObjectKey{Namespace: gatewayNamespace, Name: gatewayroutes.MarketsName}, &config); err != nil {
		return nil
	}
	var body struct {
		Markets []struct {
			ID string `json:"id"`
		} `json:"markets"`
	}
	require.NoError(t, json.Unmarshal([]byte(config.Data["response.body"]), &body))
	ids := make([]string, 0, len(body.Markets))
	for _, market := range body.Markets {
		ids = append(ids, market.ID)
	}
	return ids
}

// AC-1001, AC-1004, AC-806.
func TestADeclaredMarketIsPublished(t *testing.T) {
	ctx := t.Context()
	require.NoError(t, kube.Create(ctx, newCell("cell-declared")))
	require.NoError(t, kube.Create(ctx, newMarket("declared", "cell-declared")))

	require.Eventually(t, func() bool { return readyReason(t, "declared") == string(publishroutes.ReasonRoutesApplied) }, eventually, tick)
	require.NoError(t, route(t, "declared"))
	assert.Contains(t, listed(t), "declared")

	var market v1alpha1.Market
	require.NoError(t, kube.Get(ctx, client.ObjectKey{Name: "declared"}, &market))
	assert.Equal(t, market.Generation, market.Status.ObservedGeneration)
	var cell v1alpha1.Cell
	require.NoError(t, kube.Get(ctx, client.ObjectKey{Name: "cell-declared"}, &cell))
	assert.True(t, meta.IsStatusConditionTrue(cell.Status.Conditions, v1alpha1.ConditionReady))
}

// AC-1002, AC-1005.
func TestTheAPIServerRefusesWrongValues(t *testing.T) {
	ctx := t.Context()
	wrongRouterName := newCell("cell-wrong")
	wrongRouterName.Spec.Router.Name = "Bff_1"
	wrongPort := newCell("cell-wrong")
	wrongPort.Spec.Router.Port = 0
	wrongCellName := newCell("c")
	lowerCurrency := newMarket("wrong-market", "cell-1")
	lowerCurrency.Spec.Currency = "eur"
	emptyZone := newMarket("wrong-market", "cell-1")
	emptyZone.Spec.TimeZone = ""
	spaced := newMarket("wrong-market", "cell-1")
	spaced.Spec.DisplayName = " Berlin "
	wrongMarketName := newMarket("9market", "cell-1")

	for name, object := range map[string]client.Object{
		"router name":  wrongRouterName,
		"router port":  wrongPort,
		"cell name":    wrongCellName,
		"currency":     lowerCurrency,
		"time zone":    emptyZone,
		"display name": spaced,
		"market name":  wrongMarketName,
	} {
		t.Run(name, func(t *testing.T) {
			assert.True(t, apierrors.IsInvalid(kube.Create(ctx, object, client.DryRunAll)))
		})
	}
}

// AC-1003, AC-1006.
func TestTheSpecDoesNotChange(t *testing.T) {
	ctx := t.Context()
	cell := newCell("cell-immutable")
	market := newMarket("immutable", "cell-immutable")
	require.NoError(t, kube.Create(ctx, cell))
	require.NoError(t, kube.Create(ctx, market))

	changedPort := cell.DeepCopy()
	changedPort.Spec.Router.Port = 3011
	assert.True(t, apierrors.IsInvalid(kube.Update(ctx, changedPort, client.DryRunAll)))
	assert.NoError(t, kube.Update(ctx, cell.DeepCopy(), client.DryRunAll), "the same values change nothing")

	for field, change := range map[string]func(*v1alpha1.MarketSpec){
		"cellRef":     func(spec *v1alpha1.MarketSpec) { spec.CellRef = "cell-other" },
		"currency":    func(spec *v1alpha1.MarketSpec) { spec.Currency = "USD" },
		"timeZone":    func(spec *v1alpha1.MarketSpec) { spec.TimeZone = "Europe/Paris" },
		"displayName": func(spec *v1alpha1.MarketSpec) { spec.DisplayName = "Other" },
	} {
		changed := market.DeepCopy()
		change(&changed.Spec)
		assert.True(t, apierrors.IsInvalid(kube.Update(ctx, changed, client.DryRunAll)), field)
	}
}

// AC-1007, AC-1008, AC-1009.
func TestAnArchivedMarketLeavesTheGatewayAndKeepsItsName(t *testing.T) {
	ctx := t.Context()
	cell := newCell("cell-archive")
	market := newMarket("archived", "cell-archive")
	require.NoError(t, kube.Create(ctx, cell))
	require.NoError(t, kube.Create(ctx, market))
	require.Eventually(t, func() bool { return route(t, "archived") == nil }, eventually, tick)

	assert.Error(t, kube.Delete(ctx, market), "a Market is not deleted")
	assert.Error(t, kube.Delete(ctx, cell), "a Cell is not deleted")

	market.Spec.Archived = true
	require.NoError(t, kube.Update(ctx, market))
	require.Eventually(t, func() bool { return apierrors.IsNotFound(route(t, "archived")) }, eventually, tick)
	require.Eventually(t, func() bool { return readyReason(t, "archived") == string(publishroutes.ReasonArchived) }, eventually, tick)
	assert.NotContains(t, listed(t), "archived")

	// The status moved on: read the current version, or the update is a conflict.
	require.NoError(t, kube.Get(ctx, client.ObjectKey{Name: "archived"}, market))
	market.Spec.Archived = false
	assert.True(t, apierrors.IsInvalid(kube.Update(ctx, market, client.DryRunAll)))
	assert.True(t, apierrors.IsAlreadyExists(kube.Create(ctx, newMarket("archived", "cell-archive"))))
}

// AC-1010.
func TestAMarketWaitsForItsCell(t *testing.T) {
	ctx := t.Context()
	require.NoError(t, kube.Create(ctx, newMarket("early", "cell-late")))
	require.Eventually(t, func() bool { return readyReason(t, "early") == string(publishroutes.ReasonCellNotFound) }, eventually, tick)
	assert.True(t, apierrors.IsNotFound(route(t, "early")))

	require.NoError(t, kube.Create(ctx, newCell("cell-late")))
	require.Eventually(t, func() bool { return readyReason(t, "early") == string(publishroutes.ReasonRoutesApplied) }, eventually, tick)
	assert.NoError(t, route(t, "early"))
}

// AC-1011.
func TestAnUnknownTimeZoneDoesNotStopTheOthers(t *testing.T) {
	ctx := t.Context()
	require.NoError(t, kube.Create(ctx, newCell("cell-zone")))
	martian := newMarket("martian", "cell-zone")
	martian.Spec.TimeZone = "Mars/Olympus"
	require.NoError(t, kube.Create(ctx, martian))
	require.NoError(t, kube.Create(ctx, newMarket("earthling", "cell-zone")))

	require.Eventually(t, func() bool { return readyReason(t, "martian") == string(publishroutes.ReasonInvalidTimeZone) }, eventually, tick)
	require.Eventually(t, func() bool { return route(t, "earthling") == nil }, eventually, tick)
	assert.True(t, apierrors.IsNotFound(route(t, "martian")))
	assert.NotContains(t, listed(t), "martian")
}

// AC-808.
func TestMarketsDeclaredTogetherAreAllPublished(t *testing.T) {
	ctx := t.Context()
	require.NoError(t, kube.Create(ctx, newCell("cell-together")))
	names := []string{"together-a", "together-b", "together-c", "together-d"}
	for _, name := range names {
		go func() { assert.NoError(t, kube.Create(ctx, newMarket(name, "cell-together"))) }()
	}
	require.Eventually(t, func() bool {
		ids := listed(t)
		for _, name := range names {
			if !slices.Contains(ids, name) || route(t, name) != nil {
				return false
			}
		}
		return true
	}, eventually, tick)
}

// AC-1015: drift goes back without a restart.
func TestADeletedRouteComesBack(t *testing.T) {
	ctx := t.Context()
	require.NoError(t, kube.Create(ctx, newCell("cell-drift")))
	require.NoError(t, kube.Create(ctx, newMarket("drift", "cell-drift")))
	require.Eventually(t, func() bool { return route(t, "drift") == nil }, eventually, tick)

	object := &unstructured.Unstructured{}
	object.SetGroupVersionKind(gatewayroutes.GVKHTTPRoute)
	object.SetNamespace(gatewayNamespace)
	object.SetName(gatewayroutes.RouteName("drift"))
	require.NoError(t, kube.Delete(ctx, object))

	require.Eventually(t, func() bool { return route(t, "drift") == nil }, eventually, tick)
}

// A hand-edited list of Markets goes back too: a ConfigMap has no generation.
func TestAnEditedListComesBack(t *testing.T) {
	ctx := t.Context()
	var config corev1.ConfigMap
	require.Eventually(t, func() bool {
		return kube.Get(ctx, client.ObjectKey{Namespace: gatewayNamespace, Name: gatewayroutes.MarketsName}, &config) == nil
	}, eventually, tick)

	config.Data["response.body"] = `{"markets":[{"id":"forged"}]}`
	require.NoError(t, kube.Update(ctx, &config))

	require.Eventually(t, func() bool {
		var current corev1.ConfigMap
		require.NoError(t, kube.Get(ctx, client.ObjectKey{Namespace: gatewayNamespace, Name: gatewayroutes.MarketsName}, &current))
		return current.Data["response.body"] != `{"markets":[{"id":"forged"}]}`
	}, eventually, tick)
}
