package errorkind_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/batazor/nomnom/boundaries/controlplane/pkg/authz"
	"github.com/batazor/nomnom/boundaries/controlplane/pkg/usecase"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/applications/errorkind"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell"
	cellvo "github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/cell/vo"
	"github.com/batazor/nomnom/boundaries/controlplane/slices/topology/domains/market"
)

func TestClassify(t *testing.T) {
	for err, kind := range map[error]usecase.ErrorKind{
		authz.FailClosed(fmt.Errorf("spicedb: %w", usecase.ErrDependencyUnavailable)): usecase.KindUnavailable,
		usecase.InvalidInput("subject", "unknown kind"):                               usecase.KindInvalidInput,
		fmt.Errorf("parse: %w", cellvo.ErrInvalidValue):                               usecase.KindInvalidInput,
		authz.FailClosed(authz.ErrPermissionDenied):                                   usecase.KindPermission,
		fmt.Errorf("register: %w", cell.ErrAlreadyRegistered):                         usecase.KindAlreadyExists,
		market.ErrAlreadyCreated:                                                      usecase.KindAlreadyExists,
		fmt.Errorf("create: %w", market.ErrUnknownCell):                               usecase.KindDomainRule,
		market.ErrArchived: usecase.KindAlreadyExists,
		fmt.Errorf("archive: %w", market.ErrNotFound): usecase.KindNotFound,
		errors.New("boom"): usecase.KindInternal,
	} {
		assert.Equal(t, kind, errorkind.Classify(err), err.Error())
	}
}
