package cellrouter

import (
	"context"
	"errors"
	"sync"

	"google.golang.org/grpc"
)

// Dial opens a lazy connection to the internal gateway of one Cell for one
// audience; gateway.Dial with the outgoing credentials of BFF in production.
type Dial func(address, audience string) (*grpc.ClientConn, error)

// Router hands slices one connection per audience that picks the Cell on every
// call. Connections to Cell gateways are created on first use and kept, one
// per gateway address and audience.
type Router struct {
	placements *Placements
	dial       Dial
	mu         sync.Mutex
	conns      map[connKey]*grpc.ClientConn
}

type connKey struct {
	address  string
	audience string
}

func NewRouter(placements *Placements, dial Dial) *Router {
	return &Router{placements: placements, dial: dial, conns: map[connKey]*grpc.ClientConn{}}
}

// Conn is the connection of slices to the context of audience.
func (r *Router) Conn(audience string) grpc.ClientConnInterface {
	return &cellConn{router: r, audience: audience}
}

// Close closes every Cell connection.
func (r *Router) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	for key, conn := range r.conns {
		errs = append(errs, conn.Close())
		delete(r.conns, key)
	}
	return errors.Join(errs...)
}

// route resolves the Cell connection for the Market of ctx.
func (r *Router) route(ctx context.Context, audience string) (*grpc.ClientConn, error) {
	market, err := MarketFrom(ctx)
	if err != nil {
		return nil, err
	}
	address, err := r.placements.Gateway(market)
	if err != nil {
		return nil, err
	}
	key := connKey{address: address, audience: audience}
	r.mu.Lock()
	defer r.mu.Unlock()
	if conn, ok := r.conns[key]; ok {
		return conn, nil
	}
	conn, err := r.dial(address, audience)
	if err != nil {
		return nil, err
	}
	r.conns[key] = conn
	return conn, nil
}

// cellConn is the grpc.ClientConnInterface of one audience.
type cellConn struct {
	router   *Router
	audience string
}

func (c *cellConn) Invoke(ctx context.Context, method string, args, reply any, opts ...grpc.CallOption) error {
	conn, err := c.router.route(ctx, c.audience)
	if err != nil {
		return err
	}
	return conn.Invoke(ctx, method, args, reply, opts...)
}

func (c *cellConn) NewStream(ctx context.Context, desc *grpc.StreamDesc, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	conn, err := c.router.route(ctx, c.audience)
	if err != nil {
		return nil, err
	}
	return conn.NewStream(ctx, desc, method, opts...)
}
