package application

// ErrorKind is the class of a use case error that inbound adapters translate
// with their own tables: gRPC codes, the consumer error class, Activity
// retryability and metric labels. A slice classifies its errors in one
// function; each adapter table covers every kind.
type ErrorKind string

const (
	// KindInvalidInput: the Command, Query or a value in it is malformed.
	KindInvalidInput ErrorKind = "invalid_input"
	// KindNotFound: an addressed resource does not exist.
	KindNotFound ErrorKind = "not_found"
	// KindAlreadyExists: the resource to create exists already.
	KindAlreadyExists ErrorKind = "already_exists"
	// KindConflict: a concurrent change won; the call may be retried.
	KindConflict ErrorKind = "conflict"
	// KindDomainRule: the domain refused the operation in the current state.
	KindDomainRule ErrorKind = "domain_rule"
	// KindUnavailable: a dependency did not answer; the call may be retried.
	KindUnavailable ErrorKind = "unavailable"
	// KindPermission: the caller may not perform the operation.
	KindPermission ErrorKind = "permission"
	// KindInternal: any other failure.
	KindInternal ErrorKind = "internal"
)

// ErrorKinds lists every kind; adapter tests check their tables against it.
func ErrorKinds() []ErrorKind {
	return []ErrorKind{
		KindInvalidInput, KindNotFound, KindAlreadyExists, KindConflict,
		KindDomainRule, KindUnavailable, KindPermission, KindInternal,
	}
}
