// Package controller runs the publication of routes as a reconcile loop (ADR
// 41). One key stands for the whole topology: an event of a Cell, a Market or a
// managed resource of the public gateway queues it, so publications never run in
// parallel and a later event always applies a newer snapshot. Nothing polls.
package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/batazor/nomnom/boundaries/controlplane/infrastructure/kubernetes/api/v1alpha1"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/infrastructure/gatewayroutes"
)

const (
	name = "topology"
	// A failed publication is repeated by this backoff; its cap bounds how long
	// the gateway stays behind a recovered Kubernetes API.
	retryBaseDelay = time.Second
	retryMaxDelay  = 30 * time.Second
)

// topologyKey is the only key of the queue: a publication covers the whole
// topology, whichever event queued it.
var topologyKey = reconcile.Request{NamespacedName: types.NamespacedName{Name: name}}

// Publisher is the publication the loop repeats.
type Publisher interface {
	Publish(ctx context.Context) error
}

// NewManager creates the manager of the process: its cache sees the Cells and
// Markets of the cluster and only the managed resources of the namespace of the
// public gateway, which is all the RBAC of the control plane allows. Telemetry
// goes by OTLP, so the Prometheus endpoint of the manager is off.
func NewManager(restConfig *rest.Config, namespace string, shutdownTimeout time.Duration) (manager.Manager, error) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register registry types: %w", err)
	}
	managed := cache.ByObject{
		Namespaces: map[string]cache.Config{namespace: {}},
		Label:      labels.SelectorFromSet(labels.Set{gatewayroutes.LabelManagedBy: gatewayroutes.FieldManager}),
	}
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:                  scheme,
		Metrics:                 metricsserver.Options{BindAddress: "0"},
		GracefulShutdownTimeout: &shutdownTimeout,
		Cache: cache.Options{ByObject: map[client.Object]cache.ByObject{
			managedObject(gatewayroutes.GVKHTTPRoute):       managed,
			managedObject(gatewayroutes.GVKHTTPRouteFilter): managed,
			managedObject(gatewayroutes.GVKConfigMap):       managed,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("create manager: %w", err)
	}
	return mgr, nil
}

// Register adds the loop to the manager. The channel closes after the first
// successful publication.
func Register(mgr manager.Manager, publisher Publisher) (<-chan struct{}, error) {
	published := make(chan struct{})
	queue := handler.EnqueueRequestsFromMapFunc(func(context.Context, client.Object) []reconcile.Request {
		return []reconcile.Request{topologyKey}
	})
	// A status write changes no generation: the loop does not wake itself.
	specChanged := builder.WithPredicates(predicate.GenerationChangedPredicate{})
	err := ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(ctrlcontroller.Options{
			MaxConcurrentReconciles: 1,
			RateLimiter:             workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](retryBaseDelay, retryMaxDelay),
		}).
		Watches(&v1alpha1.Cell{}, queue, specChanged).
		Watches(&v1alpha1.Market{}, queue, specChanged).
		// Envoy Gateway writes the status of routes, which is no drift; a ConfigMap
		// has no generation, and applying the same data does not change it.
		Watches(managedObject(gatewayroutes.GVKHTTPRoute), queue, specChanged).
		Watches(managedObject(gatewayroutes.GVKHTTPRouteFilter), queue, specChanged).
		Watches(managedObject(gatewayroutes.GVKConfigMap), queue).
		// An empty registry has no event, and the list of Markets still has to exist.
		WatchesRawSource(source.Func(func(_ context.Context, q workqueue.TypedRateLimitingInterface[reconcile.Request]) error {
			q.Add(topologyKey)
			return nil
		})).
		Complete(&reconciler{publisher: publisher, published: published})
	if err != nil {
		return nil, fmt.Errorf("register controller: %w", err)
	}
	return published, nil
}

func managedObject(gvk schema.GroupVersionKind) *unstructured.Unstructured {
	object := &unstructured.Unstructured{}
	object.SetGroupVersionKind(gvk)
	return object
}

type reconciler struct {
	publisher Publisher
	published chan struct{}
	once      sync.Once
}

func (r *reconciler) Reconcile(ctx context.Context, _ reconcile.Request) (reconcile.Result, error) {
	if err := r.publisher.Publish(ctx); err != nil {
		return reconcile.Result{}, fmt.Errorf("publish routes: %w", err)
	}
	r.once.Do(func() { close(r.published) })
	return reconcile.Result{}, nil
}
