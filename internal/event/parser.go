package event

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	combinedRE = regexp.MustCompile(
		`^(\S+) \S+ \S+ \[([^\]]+)\] "` + quotedLogField + `" (\d{3}) (\S+) "` + quotedLogField + `" "` + quotedLogField + `"(?: "` + quotedLogField + `")?$`,
	)
	commonRE = regexp.MustCompile(
		`^(\S+) \S+ \S+ \[([^\]]+)\] "` + quotedLogField + `" (\d{3}) (\S+)$`,
	)
	errorRE = regexp.MustCompile(
		`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}) \[(\w+)\] (\d+)#(\d+): (?:\*([0-9]+) )?(.*)$`,
	)
)

const commonLogTime = "02/Jan/2006:15:04:05 -0700"

func parseTime(value string) (time.Time, error) {
	return time.Parse(commonLogTime, value)
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

// Some proxies record zero when the connection closes before a response.
func loggedStatus(value string) (int, error) {
	if value == "0" || value == "000" {
		return 0, nil
	}
	return accessStatus(value)
}

func parseIP(value string) string {
	ip := net.ParseIP(value)
	if ip == nil {
		return ""
	}
	return ip.String()
}

func ParseAccess(line, format string) (AccessEvent, error) {
	parse, err := NewAccessParser(format, "", "", nil)
	if err != nil {
		return AccessEvent{}, err
	}
	return parse(line)
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
	e.Status, err = loggedStatus(match[4])
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
		e.Referer, e.UserAgent = match[6], match[7]
	}
	if len(match) > 8 {
		e.XForwardedFor = match[8]
	}
	return e, nil
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

func parseAccessRequest(value string) (method, url, protocol string) {
	method, rest, ok := strings.Cut(value, " ")
	if !ok || method == "" || rest == "" {
		return "", "", ""
	}
	if i := strings.LastIndex(rest, " HTTP/"); i >= 0 {
		return method, rest[:i], rest[i+1:]
	}
	return method, rest, ""
}

func parseAccessTime(value string) (time.Time, error) {
	for _, layout := range []string{
		time.RFC3339Nano, commonLogTime,
		"2006-01-02T15:04:05.999999999Z0700", "2006/01/02 15:04:05",
	} {
		if t, err := time.Parse(layout, value); err == nil && !t.IsZero() {
			return t, nil
		}
	}
	if t, ok := parseEpoch(value); ok {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid timestamp")
}

func parseEpoch(value string) (time.Time, bool) {
	integer, fraction, hasFraction := strings.Cut(value, ".")
	n, err := strconv.ParseInt(integer, 10, 64)
	if err != nil || n < 0 || (hasFraction && fraction == "") {
		return time.Time{}, false
	}
	unit := int64(1)
	switch digits := len(strings.TrimLeft(integer, "0")); {
	case digits > 16:
		unit = 1_000_000_000
	case digits > 13:
		unit = 1_000_000
	case digits > 10:
		unit = 1_000
	}
	// Preserve nanoseconds without converting a large timestamp to float64.
	scale := int64(time.Second) / unit
	nanos := (n % unit) * scale
	for _, digit := range fraction {
		if digit < '0' || digit > '9' {
			return time.Time{}, false
		}
		scale /= 10
		nanos += int64(digit-'0') * scale
	}
	return time.Unix(n/unit, nanos).UTC(), true
}
