package errorkind_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/application"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/errorkind"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell"
	cellvo "github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
)

func TestClassify(t *testing.T) {
	for err, kind := range map[error]application.ErrorKind{
		application.FailClosed(fmt.Errorf("spicedb: %w", application.ErrDependencyUnavailable)): application.KindUnavailable,
		application.InvalidInput("subject", "unknown kind"):                                     application.KindInvalidInput,
		fmt.Errorf("parse: %w", cellvo.ErrInvalidValue):                                         application.KindInvalidInput,
		application.FailClosed(application.ErrPermissionDenied):                                 application.KindPermission,
		fmt.Errorf("register: %w", cell.ErrAlreadyRegistered):                                   application.KindAlreadyExists,
		market.ErrAlreadyCreated:                                                                application.KindAlreadyExists,
		fmt.Errorf("create: %w", market.ErrUnknownCell):                                         application.KindDomainRule,
		errors.New("boom"): application.KindInternal,
	} {
		assert.Equal(t, kind, errorkind.Classify(err), err.Error())
	}
}
