package main

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/gateway"
)

// recordingDial dials lazily, as in production, and records the audiences.
type recordingDial struct {
	audiences    []string
	callTimeouts []time.Duration
	err          error
}

func (d *recordingDial) dial(address, audience string, callTimeout time.Duration, credentials identity.OutgoingCredentials) (*grpc.ClientConn, error) {
	d.audiences = append(d.audiences, audience)
	d.callTimeouts = append(d.callTimeouts, callTimeout)
	if d.err != nil {
		return nil, d.err
	}
	return gateway.Dial(address, audience, callTimeout, credentials)
}

func identityConfig(t *testing.T) identity.Config {
	t.Helper()
	return identity.Config{
		IdPProviders:     `[{"issuer":"https://idp.nomnom.test","jwksFile":"` + filepath.Join(fixtures, "idp-jwks.json") + `"}]`,
		IdPAudience:      "nomnom-bff",
		AssertionKeyFile: filepath.Join(fixtures, "bff-signing-key.pem"),
		AssertionKeyID:   "nomnom-bff-test-key",
		ServiceTokenDir:  t.TempDir(),
	}
}

func TestConfigIsReadOnce(t *testing.T) {
	reads := map[string]int{}
	cfg := configFromEnv(func(name string) string {
		reads[name]++
		return "value-of-" + name
	})
	assert.Equal(t, "value-of-"+envInternalGateway, cfg.internalGateway)
	assert.Equal(t, "value-of-"+identity.EnvIdPProviders, cfg.identity.IdPProviders)
	for name, count := range reads {
		assert.Equal(t, 1, count, name)
	}
}

// ADR 41: BFF serves one Cell — one connection per context, all through the
// internal gateway of the Cell; the control plane is not called.
func TestWiringDialsTheContextsOfTheCell(t *testing.T) {
	dial := &recordingDial{}
	bff, err := newBffService(validConfig(t), dial.dial)
	require.NoError(t, err)
	assert.Equal(t, []string{audienceOrders, audienceRestaurants, audienceDelivery}, dial.audiences)
	assert.Equal(t, []time.Duration{5 * time.Second, 5 * time.Second, 5 * time.Second}, dial.callTimeouts)
	assert.NotNil(t, bff.accessTokens)
	assert.NotNil(t, bff.tracking)
	assert.NotNil(t, bff.catalog)
	assert.NotNil(t, bff.checkout)
	assert.NotNil(t, bff.management)
	assert.NotNil(t, bff.simulator)
	assert.NotNil(t, bff.kitchen)
	assert.NotNil(t, bff.courier)
	assert.NotNil(t, bff.courierPool)

	require.NoError(t, bff.conns.close(), "connections close")
}

// validConfig is a complete configuration of the plugin.
func validConfig(t *testing.T) config {
	t.Helper()
	return config{
		identity:              identityConfig(t),
		internalGateway:       "gateway:8080",
		callTimeout:           "5s",
		courierStartLatitude:  "52.5250",
		courierStartLongitude: "13.4020",
	}
}

// FR-623: the start point of new couriers is required and validated.
func TestWiringRequiresTheCourierStartPoint(t *testing.T) {
	for _, point := range [][2]string{{"", "13.4"}, {"52.5", ""}, {"north", "13.4"}, {"91", "13.4"}, {"52.5", "181"}} {
		cfg := validConfig(t)
		cfg.courierStartLatitude, cfg.courierStartLongitude = point[0], point[1]
		_, err := newBffService(cfg, (&recordingDial{}).dial)
		assert.Error(t, err, "%v", point)
	}
}

// ADR 15: every call to a context is bounded; the bound is required.
func TestWiringRequiresTheCallTimeout(t *testing.T) {
	for _, timeout := range []string{"", "5", "soon", "0s", "-1s"} {
		cfg := validConfig(t)
		cfg.callTimeout = timeout
		_, err := newBffService(cfg, (&recordingDial{}).dial)
		assert.ErrorContains(t, err, envCallTimeout, "%q", timeout)
	}
}

func TestWiringRequiresIdentityAndGateway(t *testing.T) {
	_, err := newBffService(config{internalGateway: "gateway:8080"}, (&recordingDial{}).dial)
	assert.Error(t, err, "no identity")

	_, err = newBffService(config{identity: identityConfig(t)}, (&recordingDial{}).dial)
	assert.ErrorContains(t, err, envInternalGateway)
}

func TestWiringReturnsErrorsInsteadOfStopping(t *testing.T) {
	_, err := newBffService(config{identity: identity.Config{IdPAudience: "nomnom-bff"}}, (&recordingDial{}).dial)
	assert.Error(t, err, "partial identity")

	down := errors.New("dial failed")
	_, err = newBffService(validConfig(t), (&recordingDial{err: down}).dial)
	assert.ErrorIs(t, err, down)
}
