package home

import "time"

// AtTime returns a store that reads the department of the day at now.
func (s *Store) AtTime(now time.Time) *Store {
	c := *s
	c.now = func() time.Time { return now }
	return &c
}
