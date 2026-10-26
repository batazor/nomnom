package fake

import (
	"context"
	"fmt"
	"sync"

	"nomnom/orders/pkg/application"
	"nomnom/orders/slices/order/applications/syncorderrelationships"
	"nomnom/orders/slices/order/domains/order"
)

// RelationshipSync is a stand-in for the SpiceDB relationship sync: it keeps
// the last synced Subjects of each Order.
type RelationshipSync struct {
	mu     sync.Mutex
	synced map[order.ID]syncorderrelationships.Subjects
	syncs  int
}

func NewRelationshipSync() *RelationshipSync {
	return &RelationshipSync{synced: make(map[order.ID]syncorderrelationships.Subjects)}
}

func (r *RelationshipSync) SyncOrderRelationships(_ context.Context, orderID order.ID, subjects syncorderrelationships.Subjects) (application.AuthorizationRevision, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.synced[orderID] = subjects
	r.syncs++
	return application.AuthorizationRevision(fmt.Sprintf("fake:%s:%d", orderID, r.syncs)), nil
}

// Synced returns the last synced Subjects of an Order.
func (r *RelationshipSync) Synced(orderID order.ID) (syncorderrelationships.Subjects, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	subjects, ok := r.synced[orderID]
	return subjects, ok
}
