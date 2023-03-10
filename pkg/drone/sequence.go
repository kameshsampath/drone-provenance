package drone

import "sync"

// sequence provides a thread-safe counter.
type sequence struct {
	sync.Mutex
	value int
}

// next returns the next sequence value.
func (s *sequence) next() int {
	s.Lock()
	s.value++
	i := s.value
	s.Unlock()
	return i
}

// curr returns the current sequence value.
func (s *sequence) curr() int {
	s.Lock()
	i := s.value
	s.Unlock()
	return i
}
