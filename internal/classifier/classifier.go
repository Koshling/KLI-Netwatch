package classifier

import (
	"net"
	"strings"
	"time"

	"netwatch/internal/event"
)

type Category string

const (
	User   Category = "USER"
	Bot    Category = "BOT"
	Threat Category = "THREAT"
)

type clientState struct {
	category Category
	next     int
	requests []int64
}

type Classifier struct {
	clients map[string]*clientState
}

func New() *Classifier {
	return &Classifier{clients: make(map[string]*clientState)}
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

func classifyEvent(e event.AccessEvent, state *clientState, now time.Time) Category {
	path, _, _ := strings.Cut(e.URL, "?")

	path = strings.ToLower(path)
	for _, pattern := range suspiciousPaths {
		if strings.Contains(path, pattern) {
			return Threat
		}
	}

	// Высокая частота сама по себе не делает клиента угрозой.
	// Учитываем её только вместе с серией ответов 403/404.
	// Only the most recent 40 timestamps can establish the threshold.
	stamp := now.UnixNano()
	if n := len(state.requests); n > 0 {
		last := (state.next + n - 1) % n
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
	if len(state.requests) < 40 {
		state.requests = append(state.requests, stamp)
	} else {
		state.requests[state.next] = stamp
		state.next = (state.next + 1) % 40
	}
	if len(state.requests) == 40 && (e.Status == 403 || e.Status == 404) {
		// Log entries may arrive out of order. Check both ends of the window.
		burst := true
		for _, previous := range state.requests {
			if previous > stamp || expired(stamp, previous) {
				burst = false
				break
			}
		}
		if burst {
			return Threat
		}
	}

	ua := strings.ToLower(e.UserAgent)
	for _, agent := range botAgents {
		if strings.Contains(ua, agent) {
			return Bot
		}
	}

	return User
}

func expired(now, previous int64) bool {
	return previous <= now && uint64(now)-uint64(previous) > uint64(10*time.Second)
}

func priority(c Category) int {
	switch c {
	case Threat:
		return 3
	case Bot:
		return 2
	default:
		return 1
	}
}

// Classify сохраняет наиболее подозрительную категорию клиента
// в течение текущего запуска NETWATCH.
func (c *Classifier) Classify(e event.AccessEvent) Category {
	if e.ClientIP == "" || net.ParseIP(e.ClientIP) == nil {
		return User
	}

	now := e.Time
	if now.IsZero() {
		now = time.Now()
	}

	state := c.clients[e.ClientIP]
	if state == nil {
		state = &clientState{category: User}
		c.clients[e.ClientIP] = state
	}

	if state.category == Threat {
		return Threat
	}
	detected := classifyEvent(e, state, now)
	if priority(detected) > priority(state.category) {
		state.category = detected
		if detected == Threat {
			state.requests = nil
		}
	}

	return state.category
}
