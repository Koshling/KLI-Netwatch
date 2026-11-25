package classifier

import (
	"strings"
	"time"

	"netwatch/internal/event"
)

// MaxClients bounds per-session classification and distinct-client accounting.
const MaxClients = 65536

type Category string

const (
	User   Category = "USER"
	Bot    Category = "BOT"
	Threat Category = "THREAT"
)

const (
	botClient    uint8 = 1
	threatClient uint8 = 2
)

// Client holds the bounded classification history of one client.
type Client struct {
	requests []int64
	category uint8
	next     uint8
}

func (c *Client) Category() Category {
	switch c.category {
	case botClient:
		return Bot
	case threatClient:
		return Threat
	default:
		return User
	}
}

var botAgents = []string{
	"googlebot", "bingbot", "duckduckbot", "yandexbot",
	"baiduspider", "applebot", "uptime-kuma",
	"prometheus", "healthcheck", "crawler", "spider",
}

var suspiciousPaths = []string{
	"/.env", "/.git", "/.aws", "/.ssh",
	"wp-config.php", "xmlrpc.php", "phpmyadmin",
	"/vendor/phpunit", "/actuator", "/server-status",
	"/cgi-bin/", "/.htaccess", "/.htpasswd",
}

func classifyEvent(e event.AccessEvent, state *Client, now time.Time) Category {
	path, _, _ := strings.Cut(e.URL, "?")

	path = strings.ToLower(path)
	for _, pattern := range suspiciousPaths {
		if strings.Contains(path, pattern) {
			return Threat
		}
	}

	if state != nil && errorBurst(e, state, now) {
		return Threat
	}
	ua := strings.ToLower(e.UserAgent)
	for _, agent := range botAgents {
		if strings.Contains(ua, agent) {
			return Bot
		}
	}
	return User
}

func errorBurst(e event.AccessEvent, state *Client, now time.Time) bool {
	// Высокая частота сама по себе не делает клиента угрозой.
	// Учитываем её только вместе с серией ответов 403/404.
	// Only the most recent 40 timestamps can establish the threshold.
	stamp := now.UnixNano()
	if n := len(state.requests); n > 0 {
		last := (int(state.next) + n - 1) % n
		if expired(stamp, state.requests[last]) {
			allExpired := true
			for _, previous := range state.requests {
				if !expired(stamp, previous) {
					allExpired = false
					break
				}
			}
			if allExpired {
				state.requests = state.requests[:0]
				state.next = 0
			}
		}
	}
	const burstSize = 40
	if len(state.requests) < burstSize {
		if len(state.requests) == cap(state.requests) && cap(state.requests) > burstSize/2 {
			requests := make([]int64, len(state.requests), burstSize)
			copy(requests, state.requests)
			state.requests = requests
		}
		state.requests = append(state.requests, stamp)
	} else {
		state.requests[state.next] = stamp
		state.next = (state.next + 1) % burstSize
	}
	if len(state.requests) == burstSize && (e.Status == 403 || e.Status == 404) {
		// Log entries may arrive out of order. Check both ends of the window.
		burst := true
		for _, previous := range state.requests {
			if previous > stamp || expired(stamp, previous) {
				burst = false
				break
			}
		}
		if burst {
			return true
		}
	}
	return false
}

func expired(now, previous int64) bool {
	return previous <= now && uint64(now)-uint64(previous) > uint64(10*time.Second)
}

// Classify retains the highest category seen for a tracked client.
func (c *Client) Classify(e event.AccessEvent) Category {
	if c != nil && c.Category() == Threat {
		return Threat
	}
	now := e.Time
	if now.IsZero() {
		now = time.Now()
	}
	detected := classifyEvent(e, c, now)
	if c == nil {
		return detected
	}
	switch detected {
	case Threat:
		c.category = threatClient
		c.requests = nil
	case Bot:
		c.category = botClient
	}
	return c.Category()
}
