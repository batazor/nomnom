// Package testfixture starts the infrastructure of integration tests once per
// test package, from TestMain:
//
//	var cluster *testfixture.K3s
//
//	func TestMain(m *testing.M) {
//		cluster = testfixture.MustStartK3s()
//		defer cluster.Close()
//		m.Run()
//	}
//
// Production code does not import this package.
package testfixture

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// K3sImage runs the Kubernetes minor of KIND_NODE_IMAGE in ops/kind/versions.env:
// CEL rules and admission policies are the API server's.
const K3sImage = "rancher/k3s:v1.37.0-k3s1"

const terminateTimeout = 30 * time.Second

// K3s is a Kubernetes API server for the tests of one package.
type K3s struct {
	Config    *rest.Config
	container testcontainers.Container
}

// StartK3s starts a k3s container. CRDs are the caller's: the fixture does not
// know them.
func StartK3s(ctx context.Context) (*K3s, error) {
	c, err := k3s.Run(ctx, K3sImage)
	if err != nil {
		return nil, terminate(c, fmt.Errorf("start k3s: %w", err))
	}
	kubeconfig, err := c.GetKubeConfig(ctx)
	if err != nil {
		return nil, terminate(c, fmt.Errorf("k3s kubeconfig: %w", err))
	}
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, terminate(c, fmt.Errorf("parse k3s kubeconfig: %w", err))
	}
	return &K3s{Config: config, container: c}, nil
}

// MustStartK3s is StartK3s for TestMain: without the API server no test of the
// package can run.
func MustStartK3s() *K3s {
	cluster, err := StartK3s(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	return cluster
}

func (k *K3s) Close() { closeContainer(k.container) }

func closeContainer(c testcontainers.Container) {
	ctx, cancel := context.WithTimeout(context.Background(), terminateTimeout)
	defer cancel()
	if err := testcontainers.TerminateContainer(c, testcontainers.StopContext(ctx)); err != nil {
		log.Printf("terminate container: %v", err)
	}
}

func terminate(c testcontainers.Container, err error) error {
	closeContainer(c)
	return err
}
