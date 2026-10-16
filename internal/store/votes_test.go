package store

import "testing"

func TestNextVote(t *testing.T) {
	cases := []struct {
		prev      int
		direction string
		state     int
		delta     int
	}{
		{0, "up", 1, 1},
		{1, "up", 0, -1},
		{-1, "up", 1, 2},
		{0, "down", -1, -1},
		{-1, "down", 0, 1},
		{1, "down", -1, -2},
	}
	for _, c := range cases {
		state, delta, err := NextVote(c.prev, c.direction)
		if err != nil || state != c.state || delta != c.delta {
			t.Errorf("NextVote(%d, %q) = %d, %d, %v; want %d, %d", c.prev, c.direction, state, delta, err, c.state, c.delta)
		}
	}
	if _, delta, err := NextVote(1, "sideways"); err == nil || delta != 0 {
		t.Errorf("bad direction: delta %d err %v", delta, err)
	}
}
