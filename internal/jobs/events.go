package jobs

// Event is one server-sent update for a job.
//
// The payload is the whole job snapshot rather than a delta. That is a
// deliberate simplification: a job has a handful of steps, so a full snapshot
// is a few hundred bytes, and it makes reconnection trivial — a browser that
// drops and re-subscribes is immediately correct without replaying anything.
type Event struct {
	Job *Job `json:"job"`
	// Done marks the terminal event, after which the browser closes the stream
	// instead of letting EventSource reconnect forever.
	Done bool `json:"done"`
}

// subscribe registers a listener for a job's updates.
//
// The channel is buffered and publish never blocks on it: a slow or vanished
// browser must not be able to stall the worker that is producing the events.
func (s *Store) subscribe(id string) (<-chan Event, func()) {
	ch := make(chan Event, 8)

	s.mu.Lock()
	s.subs[id] = append(s.subs[id], ch)
	s.mu.Unlock()

	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		subs := s.subs[id]
		for i, c := range subs {
			if c == ch {
				s.subs[id] = append(subs[:i], subs[i+1:]...)
				close(c)
				return
			}
		}
	}
	return ch, cancel
}

// Subscribe exposes the subscription to the HTTP layer, returning the job's
// current state alongside the stream.
//
// Snapshot-then-subscribe closes the race where a job finishes between the
// browser loading the page and its EventSource connecting: the first thing a
// subscriber receives is always the truth as of subscription time.
func (s *Store) Subscribe(id string) (*Job, <-chan Event, func(), error) {
	j, err := s.Get(id)
	if err != nil {
		return nil, nil, nil, err
	}
	ch, cancel := s.subscribe(id)
	return j, ch, cancel, nil
}

// publish sends the current job state to every subscriber.
//
// The read lock is held across the sends, not just across the snapshot. That is
// what makes closing the channel in cancel safe: without it, a browser
// disconnecting at the moment the worker publishes would close a channel this
// function is midway through sending on, which panics. Holding the lock cannot
// deadlock here because every send is non-blocking.
func (s *Store) publish(id string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	j, ok := s.jobs[id]
	if !ok {
		return
	}
	clone := *j
	clone.Steps = append([]Step(nil), j.Steps...)

	ev := Event{
		Job:  &clone,
		Done: clone.Status == StatusDone || clone.Status == StatusFailed,
	}
	for _, ch := range s.subs[id] {
		select {
		case ch <- ev:
		default:
			// Subscriber is not keeping up. Dropping an intermediate update is
			// safe precisely because each event is a full snapshot — the next
			// one it does receive carries the complete state anyway.
		}
	}
}
