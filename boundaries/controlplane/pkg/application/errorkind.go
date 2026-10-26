package application

// ErrorKind is the class of a use case error that inbound adapters translate
// with their own tables: gRPC codes and metric labels. A slice classifies its
// errors in one function; each adapter table covers every kind.
type ErrorKind string

const (
	// KindInvalidInput: the Command, Query or a value in it is malformed.
	KindInvalidInput ErrorKind = "invalid_input"
	// KindAlreadyExists: another resource with the same id exists already.
	KindAlreadyExists ErrorKind = "already_exists"
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
	return []ErrorKind{KindInvalidInput, KindAlreadyExists, KindDomainRule, KindUnavailable, KindPermission, KindInternal}
}
