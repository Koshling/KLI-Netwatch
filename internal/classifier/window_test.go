package classifier

import (
	"testing"
	"time"

	"netwatch/internal/event"
)

func TestTrafficWindowBoundary(t *testing.T) {
	for _, tc := range []struct {
		delay time.Duration
		want  Category
	}{{10 * time.Second, Threat}, {10*time.Second + 1, User}} {
		var c Client
		e := event.AccessEvent{ClientIP: "8.8.8.8", Time: time.Unix(100, 0), Status: 200}
		for i := 0; i < 1000; i++ {
			c.Classify(e)
		}
		if n := len(c.requests); n != 40 {
			t.Fatal(n)
		}
		e.Time = e.Time.Add(tc.delay)
		e.Status = 404
		if got := c.Classify(e); got != tc.want {
			t.Fatalf("%v: got %s", tc.delay, got)
		}
	}
}
func TestDelayedError(t *testing.T) {
	var c Client
	e := event.AccessEvent{ClientIP: "8.8.8.8", Status: 200, Time: time.Unix(100, 0)}
	for range 39 {
		c.Classify(e)
	}
	e.Time = e.Time.Add(-time.Minute)
	e.Status = 404
	if c.Classify(e) != User {
		t.Fatal("future requests counted")
	}
	e.Time = time.Unix(100, 0)
	for range 39 {
		if c.Classify(e) != User {
			t.Fatal("expired request inside ring counted")
		}
	}
	if c.Classify(e) != Threat {
		t.Fatal("missed genuine burst")
	}
}
