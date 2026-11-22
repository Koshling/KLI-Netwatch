package classifier

import (
	"netwatch/internal/event"
	"testing"
	"time"
)

func TestTrafficWindowBoundary(t *testing.T) {
	for _, tc := range []struct {
		delay time.Duration
		want  Category
	}{{10 * time.Second, Threat}, {10*time.Second + 1, User}} {
		c := New()
		e := event.AccessEvent{ClientIP: "8.8.8.8", Time: time.Unix(100, 0), Status: 200}
		for i := 0; i < 1000; i++ {
			c.Classify(e)
		}
		if n := len(c.clients[e.ClientIP].requests); n != 40 {
			t.Fatal(n)
		}
		e.Time = e.Time.Add(tc.delay)
		e.Status = 404
		if got := c.Classify(e); got != tc.want {
			t.Fatalf("%v: got %s", tc.delay, got)
		}
	}
}
