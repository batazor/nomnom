package main

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/identity"
	"github.com/batazor/nomnom/boundaries/bff/plugins/bff/infrastructure/gateway"
)

// recordingDial dials lazily, as in production, and records the audiences.
type recordingDial struct {
	audiences []string
	err       error
}

func (d *recordingDial) dial(address, audience string, credentials identity.OutgoingCredentials) (*grpc.ClientConn, error) {
	d.audiences = append(d.audiences, audience)
	if d.err != nil {
		return nil, d.err
	}
	return gateway.Dial(address, audience, credentials)
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

// FR-551: one connection per audience serves every slice.
func TestWiringOpensOneConnectionPerAudience(t *testing.T) {
	dial := &recordingDial{}
	bff, err := newBffService(config{identity: identityConfig(t), internalGateway: "gateway:8080"}, dial.dial)
	require.NoError(t, err)
	assert.Equal(t, []string{audienceOrders, audienceRestaurants, audienceDelivery}, dial.audiences)
	assert.NotNil(t, bff.accessTokens)
	assert.NotNil(t, bff.tracking)
	assert.NotNil(t, bff.catalog)
	assert.NotNil(t, bff.checkout)
	assert.NotNil(t, bff.management)
	assert.NotNil(t, bff.kitchen)
	assert.NotNil(t, bff.courier)
}

func TestWiringWithoutGatewayOrIdentityLeavesSlicesUnconnected(t *testing.T) {
	dial := &recordingDial{}
	bff, err := newBffService(config{internalGateway: "gateway:8080"}, dial.dial)
	require.NoError(t, err)
	assert.Equal(t, &BffService{}, bff, "no identity")

	bff, err = newBffService(config{identity: identityConfig(t)}, dial.dial)
	require.NoError(t, err)
	assert.NotNil(t, bff.accessTokens, "customers are still authenticated")
	assert.Nil(t, bff.tracking)
	assert.Nil(t, bff.catalog)
	assert.Nil(t, bff.checkout)
	assert.Nil(t, bff.management)
	assert.Nil(t, bff.kitchen)
	assert.Nil(t, bff.courier)
	assert.Empty(t, dial.audiences)
}

func TestWiringReturnsErrorsInsteadOfStopping(t *testing.T) {
	_, err := newBffService(config{identity: identity.Config{IdPAudience: "nomnom-bff"}}, (&recordingDial{}).dial)
	assert.Error(t, err, "partial identity")

	down := errors.New("dial failed")
	_, err = newBffService(config{identity: identityConfig(t), internalGateway: "gateway:8080"}, (&recordingDial{err: down}).dial)
	assert.ErrorIs(t, err, down)
}
