package event

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

// AccessParser is compiled once per input and can be reused across log lines.
type AccessParser func(string) (AccessEvent, error)

// SampleFormats lists formats that setup can detect without user settings.
func SampleFormats() []string {
	return []string{"combined", "common", "apache-vhost", "traefik", "envoy", "caddy-console", "json"}
}

func NewAccessParser(format, template, escape string, fields map[string]string) (AccessParser, error) {
	if format != "nginx" && (template != "" || escape != "") {
		return nil, fmt.Errorf("nginx_log_format and nginx_escape require access_format nginx")
	}
	if format != "nginx" && format != "json" && len(fields) > 0 {
		return nil, fmt.Errorf("access_fields requires access_format nginx or json")
	}
	for field, source := range fields {
		if !accessField(field) || source == "" {
			return nil, fmt.Errorf("invalid access_fields entry for %q", field)
		}
	}
	switch format {
	case "combined", "main":
		return parseCombined, nil
	case "common":
		return parseCommon, nil
	case "json":
		return newJSONParser(fields)
	case "nginx":
		return newNginxParser(template, escape, fields)
	case "apache-vhost":
		return parseApacheVHost, nil
	case "traefik":
		return parseTraefik, nil
	case "envoy":
		return parseEnvoy, nil
	case "caddy-console":
		return parseCaddyConsole, nil
	default:
		return nil, fmt.Errorf("unsupported access_format: %q", format)
	}
}

func accessField(field string) bool {
	switch field {
	case "time", "client_ip", "request", "method", "url", "protocol", "status", "response_size", "referer", "user_agent", "x_forwarded_for":
		return true
	}
	return false
}

const quotedLogField = `((?:[^"\\]|\\.)*)`

var (
	traefikRE = regexp.MustCompile(`^(\S+) \S+ \S+ \[([^\]]+)\] "(.*?)" (\d{3}|0) (\S+) "(.*?)" "(.*?)"(?: (?:\d+|-) ".*?" ".*?" \d+ms)?$`)
	// Envoy's default formatter puts raw header values between quotes.
	envoyRE = regexp.MustCompile(`^\[([^\]]+)\] "(.*?)" (\d{3}|0) \S+ \S+ (\S+) \S+ \S+ "(.*?)" "(.*?)" ".*?" ".*?" ".*?"$`)
)

func parseApacheVHost(line string) (AccessEvent, error) {
	_, rest, ok := strings.Cut(line, " ")
	if !ok {
		return AccessEvent{}, fmt.Errorf("invalid Apache virtual host access line")
	}
	return parseCombined(strings.TrimLeft(rest, " "))
}

func parseTraefik(line string) (AccessEvent, error) {
	match := traefikRE.FindStringSubmatch(line)
	if match == nil {
		return AccessEvent{}, fmt.Errorf("invalid Traefik access line")
	}
	// Traefik's last four columns are routing metadata, not forwarded IPs.
	return parseText(match)
}

func parseEnvoy(line string) (AccessEvent, error) {
	match := envoyRE.FindStringSubmatch(line)
	if match == nil {
		return AccessEvent{}, fmt.Errorf("invalid Envoy access line")
	}
	e := AccessEvent{XForwardedFor: match[5], UserAgent: match[6]}
	var err error
	e.Time, err = parseAccessTime(match[1])
	if err != nil {
		return AccessEvent{}, err
	}
	// Envoy's default text has no trusted client address. Keep the header as
	// metadata and leave ClientIP unset for per-client accounting and filters.
	e.Status, err = loggedStatus(match[3])
	if err != nil {
		return AccessEvent{}, err
	}
	if match[4] != "-" {
		e.ResponseSize, err = decimalField(match[4], "response size", 63)
		if err != nil {
			return AccessEvent{}, err
		}
	}
	e.Method, e.URL, e.Protocol = parseRequest(match[2])
	if e.Method == "-" {
		e.Method = ""
	}
	if e.URL == "-" {
		e.URL = ""
	}
	if e.Protocol == "-" {
		e.Protocol = ""
	}
	return e, nil
}

func parseAddress(value string) string {
	value = strings.TrimSpace(value)
	if ip := parseIP(value); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		return ""
	}
	return parseIP(host)
}
