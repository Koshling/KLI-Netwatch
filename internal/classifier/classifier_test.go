package classifier

import (
	"testing"
	"time"

	"netwatch/internal/event"
)

func TestClassification(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name  string
		event event.AccessEvent
		want  Category
	}{
		{
			name: "ordinary visitor",
			event: event.AccessEvent{
				ClientIP:  "192.0.2.1",
				UserAgent: "Mozilla/5.0",
				URL:       "/",
				Status:    200,
				Time:      now,
			},
			want: User,
		},
		{
			name: "known bot",
			event: event.AccessEvent{
				ClientIP:  "192.0.2.2",
				UserAgent: "Googlebot",
				URL:       "/robots.txt",
				Status:    200,
				Time:      now,
			},
			want: Bot,
		},
		{
			name: "suspicious path",
			event: event.AccessEvent{
				ClientIP:  "192.0.2.3",
				UserAgent: "Mozilla/5.0",
				URL:       "/.env",
				Status:    404,
				Time:      now,
			},
			want: Threat,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c Client
			if got := c.Classify(tt.event); got != tt.want {
				t.Fatalf("Classify() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestClientCategoryEscalates(t *testing.T) {
	var c Client
	now := time.Now()

	e := event.AccessEvent{
		ClientIP:  "192.0.2.10",
		UserAgent: "Googlebot",
		URL:       "/",
		Status:    200,
		Time:      now,
	}
	if got := c.Classify(e); got != Bot {
		t.Fatalf("first category = %s, want BOT", got)
	}

	e.URL = "/.env"
	e.Time = now.Add(time.Second)
	if got := c.Classify(e); got != Threat {
		t.Fatalf("escalated category = %s, want THREAT", got)
	}

	e.URL = "/"
	e.Time = now.Add(2 * time.Second)
	if got := c.Classify(e); got != Threat {
		t.Fatalf("category downgraded to %s", got)
	}
}
