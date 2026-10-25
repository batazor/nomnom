package memory

import "context"

func (s *Store) Accept(ctx context.Context, consumer, eventID string) (bool, error) {
	defer s.lock(ctx)()
	key := inboxKey{consumer: consumer, eventID: eventID}
	if _, processed := s.state.inbox[key]; processed {
		return false, nil
	}
	s.state.inbox[key] = struct{}{}
	return true, nil
}
