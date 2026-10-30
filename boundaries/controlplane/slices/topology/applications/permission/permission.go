// Package permission is the authorization rule of the topology commands: only
// a subject with platform#manage_markets (the platform operator, ADR 18 and
// ADR 41) registers Cells, creates Markets and lists them.
package permission

import (
	"context"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/authz"
)

const (
	ManageMarkets    authz.Permission   = "manage_markets"
	ResourcePlatform authz.ResourceType = "platform"
	// PlatformID is the singleton platform object of the schema.
	PlatformID = "nomnom"
)

// RequireManageMarkets checks platform:nomnom#manage_markets of the subject and
// fails closed: an unanswered check denies the command.
func RequireManageMarkets(ctx context.Context, authorizer authz.Authorizer, subject authz.Subject) error {
	if err := subject.Validate(); err != nil {
		return err
	}
	return authz.FailClosed(authorizer.Check(ctx, authz.AccessCheck{
		Subject:    subject,
		Permission: ManageMarkets,
		Resource:   authz.Resource{Type: ResourcePlatform, ID: PlatformID},
	}))
}
