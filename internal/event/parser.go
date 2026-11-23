package event

import (
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	combinedRE = regexp.MustCompile(
		`^(\S+) \S+ \S+ \[([^\]]+)\] "([^"]*)" (\d{3}) (\S+) "([^"]*)" "([^"]*)"(?: "([^"]*)")?$`,
	)
	commonRE = regexp.MustCompile(
		`^(\S+) \S+ \S+ \[([^\]]+)\] "([^"]*)" (\d{3}) (\S+)$`,
	)
	errorRE = regexp.MustCompile(
		`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}) \[(\w+)\] (\d+)#(\d+): (?:\*([0-9]+) )?(.*)$`,
	)
)

func parseTime(value string) (time.Time, error) {
	return time.Parse("02/Jan/2006:15:04:05 -0700", value)
}

func parseRequest(value string) (string, string, string) {
	parts := strings.SplitN(value, " ", 3)
	if len(parts) != 3 {
		return "", "", ""
	}
	return parts[0], parts[1], parts[2]
}

func decimalField(value, field string, bits int) (int64, error) {
	n, err := strconv.ParseUint(value, 10, bits)
	if err != nil {
		return 0, fmt.Errorf("invalid %s", field)
	}
	return int64(n), nil
}

func accessStatus(value string) (int, error) {
	n, err := decimalField(value, "status", 16)
	if err != nil || len(value) != 3 || n < 100 || n > 599 {
		return 0, fmt.Errorf("invalid status")
	}
	return int(n), nil
}

func parseIP(value string) string {
	ip := net.ParseIP(value)
	if ip == nil {
		return ""
	}
	return ip.String()
}

func ParseAccess(line, format string) (AccessEvent, error) {
	switch format {
	case "combined", "main":
		return parseCombined(line)
	case "common":
		return parseCommon(line)
	case "json":
		return parseJSON(line)
	default:
		return AccessEvent{}, fmt.Errorf("unsupported access format: %s", format)
	}
}

func parseCombined(line string) (AccessEvent, error) {
	return parseText(combinedRE.FindStringSubmatch(line))
}

func parseCommon(line string) (AccessEvent, error) {
	return parseText(commonRE.FindStringSubmatch(line))
}

func parseText(match []string) (AccessEvent, error) {
	if match == nil {
		return AccessEvent{}, fmt.Errorf("invalid access line")
	}
	var e AccessEvent
	var err error
	e.Time, err = parseTime(match[2])
	if err != nil {
		return AccessEvent{}, fmt.Errorf("invalid timestamp")
	}
	e.ClientIP = parseIP(match[1])
	if e.ClientIP == "" {
		return AccessEvent{}, fmt.Errorf("invalid client IP")
	}
	e.Status, err = accessStatus(match[4])
	if err != nil {
		return AccessEvent{}, err
	}
	if match[5] != "-" {
		e.ResponseSize, err = decimalField(match[5], "response size", 63)
		if err != nil {
			return AccessEvent{}, err
		}
	}
	if match[3] != "-" {
		e.Method, e.URL, e.Protocol = parseRequest(match[3])
		if e.Method == "" || e.URL == "" || e.Protocol == "" {
			return AccessEvent{}, fmt.Errorf("invalid request")
		}
	}
	if len(match) > 6 {
		e.Referer, e.UserAgent, e.XForwardedFor = match[6], match[7], match[8]
	}
	return e, nil
}

func parseJSON(line string) (AccessEvent, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		return AccessEvent{}, err
	}

	var result AccessEvent
	var fieldErr error
	read := func(key string, target any) {
		if value, ok := fields[key]; ok && fieldErr == nil {
			if string(value) == "null" || json.Unmarshal(value, target) != nil {
				fieldErr = fmt.Errorf("invalid %s", key)
			}
		}
	}

	var timestamp string
	read("time", &timestamp)
	read("time_local", &timestamp)
	if fieldErr != nil || timestamp == "" {
		return AccessEvent{}, fmt.Errorf("invalid timestamp")
	}
	{
		parsed, err := time.Parse(time.RFC3339, timestamp)
		if err != nil {
			parsed, err = parseTime(timestamp)
		}
		if err != nil {
			return AccessEvent{}, fmt.Errorf("invalid JSON timestamp: %w", err)
		}
		if parsed.IsZero() {
			return AccessEvent{}, fmt.Errorf("invalid timestamp")
		}
		result.Time = parsed
	}

	read("remote_addr", &result.ClientIP)
	read("client_ip", &result.ClientIP)
	read("request_method", &result.Method)
	read("method", &result.Method)
	read("request_uri", &result.URL)
	read("url", &result.URL)
	read("server_protocol", &result.Protocol)
	numberText := func(key string) string {
		value := fields[key]
		if len(value) > 0 && value[0] == '"' {
			var text string
			read(key, &text)
			return text
		}
		return string(value)
	}
	var err error
	result.Status, err = accessStatus(numberText("status"))
	if err != nil {
		return AccessEvent{}, err
	}
	for _, key := range []string{"body_bytes_sent", "bytes_sent"} {
		if _, ok := fields[key]; ok {
			result.ResponseSize, err = decimalField(numberText(key), "response size", 63)
			if err != nil {
				return AccessEvent{}, err
			}
		}
	}
	read("http_referer", &result.Referer)
	read("referer", &result.Referer)
	read("http_user_agent", &result.UserAgent)
	read("user_agent", &result.UserAgent)
	read("http_x_forwarded_for", &result.XForwardedFor)

	if fieldErr != nil {
		return AccessEvent{}, fieldErr
	}
	result.ClientIP = parseIP(result.ClientIP)
	if result.ClientIP == "" {
		return AccessEvent{}, fmt.Errorf("invalid client IP")
	}
	return result, nil
}

func ParseError(line string) (ErrorEvent, error) {
	match := errorRE.FindStringSubmatch(line)
	if match == nil {
		return ErrorEvent{}, fmt.Errorf("invalid nginx error line")
	}

	timestamp, err := time.Parse("2006/01/02 15:04:05", match[1])
	if err != nil {
		return ErrorEvent{}, err
	}

	pid, err := strconv.Atoi(match[3])
	if err != nil {
		return ErrorEvent{}, fmt.Errorf("invalid PID: %w", err)
	}

	return ErrorEvent{
		Time:       timestamp,
		Severity:   strings.ToLower(match[2]),
		PID:        pid,
		Connection: match[5],
		Message:    match[6],
	}, nil
}
