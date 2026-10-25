package grpcadapter

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc/metadata"

	"nomnom/orders/pkg/application"
	"nomnom/orders/pkg/config"
)

const (
	// clockLeeway tolerates skew between nodes for exp and nbf (ADR 27).
	clockLeeway = 30 * time.Second
	// maxAssertionLifetime caps exp - iat of a user assertion (ADR 32).
	maxAssertionLifetime = 60 * time.Second

	// UserAssertionMetadata carries the user assertion signed by BFF (ADR 32).
	UserAssertionMetadata = "x-nomnom-user-assertion"
)

// ServiceTokenSubjectResolver composes the identity checks of a call: the
// projected ServiceAccount token (serviceTokenVerifier, ADR 27) yields the
// service subject; a call that also carries a user assertion from an allowed
// delegate (userAssertionVerifier, ADR 32) yields the asserted user.
type ServiceTokenSubjectResolver struct {
	service serviceTokenVerifier
	// user is nil when user assertions are not configured: every one is rejected.
	user *userAssertionVerifier
	now  func() time.Time
}

// NewSubjectResolver returns the token resolver when identity is configured
// and the rejecting resolver otherwise (fail closed).
func NewSubjectResolver(cfg config.IdentityConfig) (SubjectResolver, error) {
	if !cfg.Verifying() {
		slog.Warn(fmt.Sprintf("%s, %s or %s is not set: every caller is rejected",
			config.EnvIdentityIssuer, config.EnvIdentityAudience, config.EnvIdentityJWKSFile))
		return NewRejectingSubjectResolver(), nil
	}
	service, err := newServiceTokenVerifier(cfg)
	if err != nil {
		return nil, err
	}
	user, err := newUserAssertionVerifier(cfg)
	if err != nil {
		return nil, err
	}
	return &ServiceTokenSubjectResolver{service: service, user: user, now: time.Now}, nil
}

func (r *ServiceTokenSubjectResolver) Resolve(ctx context.Context) (application.Subject, error) {
	subject, err := r.resolve(ctx)
	if err != nil {
		slog.WarnContext(ctx, "caller identity rejected", "error", err)
		return application.Subject{}, ErrUnauthenticated
	}
	return subject, nil
}

func (r *ServiceTokenSubjectResolver) resolve(ctx context.Context) (application.Subject, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	service, err := r.service.verify(md, r.now)
	if err != nil {
		return application.Subject{}, err
	}
	assertions := md.Get(UserAssertionMetadata)
	if len(assertions) == 0 {
		return application.Subject{Kind: application.SubjectService, ID: service}, nil
	}
	return r.resolveUser(service, assertions)
}

// resolveUser accepts the asserted user only from an allowed delegate. The
// delegate and jti are logged for audit; the assertion itself never is.
func (r *ServiceTokenSubjectResolver) resolveUser(delegate string, assertions []string) (application.Subject, error) {
	if len(assertions) != 1 || assertions[0] == "" {
		return application.Subject{}, fmt.Errorf("%s metadata must be present once", UserAssertionMetadata)
	}
	if r.user == nil {
		return application.Subject{}, fmt.Errorf("user assertion from %s: user assertions are not configured", delegate)
	}
	if !r.user.accepts(delegate) {
		return application.Subject{}, fmt.Errorf("user assertion from %s: service is not an allowed delegate", delegate)
	}
	claims, err := r.user.verify(assertions[0], r.service.audience, r.now)
	if err != nil {
		return application.Subject{}, fmt.Errorf("user assertion from %s (jti %q): %w", delegate, claims.ID, err)
	}
	slog.Info("user assertion accepted", slog.String("delegate", delegate), slog.String("jti", claims.ID))
	return application.Subject{Kind: application.SubjectUser, ID: claims.Subject}, nil
}
