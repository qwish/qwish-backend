package notification

// Subscribe registers a new event channel for a user's notifications stream.
func (s *Service) Subscribe(userID string) chan Notification {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan Notification, 16) // buffer to handle burst events
	s.subscribers[userID] = append(s.subscribers[userID], ch)
	return ch
}

// Unsubscribe removes a subscription channel and cleans up resources.
func (s *Service) Unsubscribe(userID string, ch chan Notification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subs := s.subscribers[userID]
	for i, sub := range subs {
		if sub == ch {
			s.subscribers[userID] = append(subs[:i], subs[i+1:]...)
			close(ch)
			break
		}
	}
	if len(s.subscribers[userID]) == 0 {
		delete(s.subscribers, userID)
	}
}

// Publish distributes a notification event to all active streaming channels for a user.
func (s *Service) Publish(userID string, notif Notification) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	subs, ok := s.subscribers[userID]
	if !ok {
		return
	}
	for _, ch := range subs {
		select {
		case ch <- notif:
		default:
			// Drop message if consumer is too slow to avoid blocking the caller
		}
	}
}
