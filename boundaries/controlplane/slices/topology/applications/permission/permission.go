// Package permission is the authorization rule of the topology commands: only
// a subject with platform#manage_markets (the platform operator, ADR 18 and
// ADR 41) registers Cells and creates Markets.
package permission

import (
	"context"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
)

const (
	ManageMarkets    application.Permission   = "manage_markets"
	ResourcePlatform application.ResourceType = "platform"
	// PlatformID is the singleton platform object of the schema.
	PlatformID = "nomnom"
)

// RequireManageMarkets checks platform:nomnom#manage_markets of the subject and
// fails closed: an unanswered check denies the command.
func RequireManageMarkets(ctx context.Context, authorizer application.Authorizer, subject application.Subject) error {
	if err := subject.Validate(); err != nil {
		return err
	}
	return application.FailClosed(authorizer.Check(ctx, application.AccessCheck{
		Subject:    subject,
		Permission: ManageMarkets,
		Resource:   application.Resource{Type: ResourcePlatform, ID: PlatformID},
	}))
}
