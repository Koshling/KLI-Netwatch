package event

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func newJSONParser(fields map[string]string) (AccessParser, error) {
	if len(fields) == 0 {
		return parseJSON, nil
	}
	paths := make(map[string][]string, len(fields))
	for field, pointer := range fields {
		if !strings.HasPrefix(pointer, "/") {
			return nil, fmt.Errorf("access_fields.%s must be a JSON pointer beginning with /", field)
		}
		parts := strings.Split(pointer[1:], "/")
		for i, part := range parts {
			for j := 0; j < len(part); j++ {
				if part[j] == '~' && (j+1 == len(part) || (part[j+1] != '0' && part[j+1] != '1')) {
					return nil, fmt.Errorf("invalid JSON pointer for access_fields.%s", field)
				}
			}
			parts[i] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		}
		paths[field] = parts
	}
	return func(line string) (AccessEvent, error) {
		return parseJSONAccess(line, "", paths)
	}, nil
}

func parseJSON(line string) (AccessEvent, error) {
	return parseJSONAccess(line, "", nil)
}

func parseCaddyConsole(line string) (AccessEvent, error) {
	object := strings.Index(line, "\t{")
	if object < 0 {
		return AccessEvent{}, fmt.Errorf("invalid Caddy console access line")
	}
	timestamp, _, ok := strings.Cut(line[:object], "\t")
	if !ok || timestamp == "" {
		return AccessEvent{}, fmt.Errorf("invalid Caddy console access line")
	}
	return parseJSONAccess(line[object+1:], timestamp, nil)
}

func jsonValue(value any, path []string) any {
	for _, key := range path {
		switch node := value.(type) {
		case map[string]any:
			value = node[key]
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(node) || strconv.Itoa(i) != key {
				return nil
			}
			value = node[i]
		default:
			return nil
		}
	}
	return value
}

func parseJSONAccess(line, consoleTime string, paths map[string][]string) (AccessEvent, error) {
	d := json.NewDecoder(strings.NewReader(line))
	d.UseNumber()
	var fields map[string]any
	if err := d.Decode(&fields); err != nil || fields == nil {
		return AccessEvent{}, fmt.Errorf("invalid JSON access object")
	}
	if _, err := d.Token(); err != io.EOF {
		return AccessEvent{}, fmt.Errorf("trailing data after JSON access object")
	}

	caddy := func(key string) any {
		return jsonValue(fields["request"], []string{key})
	}
	header := func(name string) any {
		headers, _ := caddy("headers").(map[string]any)
		for key, value := range headers {
			if strings.EqualFold(key, name) {
				return value
			}
		}
		return nil
	}
	pick := func(field string, values ...any) any {
		if path, ok := paths[field]; ok {
			return jsonValue(fields, path)
		}
		for _, value := range values {
			if value != nil && value != "-" && value != "" {
				return value
			}
		}
		return nil
	}

	var e AccessEvent
	var err error
	timestamp := pick("time", fields["StartUTC"], fields["StartLocal"], fields["time_local"], fields["time"], fields["time_iso8601"],
		fields["msec"], fields["timestamp"], fields["@timestamp"], fields["ts"], fields["start_time"])
	if consoleTime != "" {
		timestamp = consoleTime
	}
	e.Time, err = parseAccessTime(jsonScalar(timestamp))
	if err != nil {
		return AccessEvent{}, err
	}
	e.ClientIP = parseAddress(jsonScalar(pick("client_ip", fields["client_ip"], fields["remote_addr"],
		caddy("client_ip"), caddy("remote_ip"), fields["ClientHost"], fields["ClientAddr"], fields["remote_ip"],
		fields["client_address"], fields["downstream_remote_address"])))
	if e.ClientIP == "" {
		return AccessEvent{}, fmt.Errorf("invalid client IP")
	}
	status := jsonScalar(pick("status", fields["status"], fields["DownstreamStatus"], fields["response_code"], fields["status_code"]))
	e.Status, err = loggedStatus(status)
	if err != nil {
		return AccessEvent{}, err
	}
	size := pick("response_size", fields["bytes_sent"], fields["body_bytes_sent"], fields["size"],
		fields["DownstreamContentSize"], fields["response_size"], fields["bytes"])
	if size != nil && size != "-" && size != "" {
		e.ResponseSize, err = decimalField(jsonScalar(size), "response size", 63)
		if err != nil {
			return AccessEvent{}, err
		}
	}

	request, _ := fields["request"].(string)
	requestLine := pick("request", fields["request_line"], request)
	if requestLine != nil && requestLine != "-" && requestLine != "" {
		e.Method, e.URL, e.Protocol = parseAccessRequest(jsonScalar(requestLine))
		if e.Method == "" || e.URL == "" {
			return AccessEvent{}, fmt.Errorf("invalid request")
		}
	}
	for _, field := range []struct {
		name  string
		dst   *string
		value any
	}{
		{"method", &e.Method, pick("method", fields["method"], fields["request_method"], caddy("method"), fields["RequestMethod"])},
		{"url", &e.URL, pick("url", fields["url"], fields["request_uri"], caddy("uri"), fields["RequestPath"], fields["path"], fields["uri"])},
		{"protocol", &e.Protocol, pick("protocol", fields["server_protocol"], caddy("proto"), fields["RequestProtocol"], fields["protocol"])},
		{"referer", &e.Referer, pick("referer", fields["referer"], fields["http_referer"], fields["request_Referer"], header("Referer"))},
		{"user_agent", &e.UserAgent, pick("user_agent", fields["user_agent"], fields["http_user_agent"], fields["request_User-Agent"], header("User-Agent"))},
		{"x_forwarded_for", &e.XForwardedFor, pick("x_forwarded_for", fields["http_x_forwarded_for"], fields["x_forwarded_for"],
			fields["request_X-Forwarded-For"], header("X-Forwarded-For"))},
	} {
		if field.value == nil {
			continue
		}
		value, ok := field.value.(string)
		if !ok && (field.name == "referer" || field.name == "user_agent" || field.name == "x_forwarded_for") {
			if items, isArray := field.value.([]any); isArray {
				value, ok = joinHeader(items)
			}
		}
		if !ok {
			return AccessEvent{}, fmt.Errorf("invalid %s", field.name)
		}
		if value != "-" && value != "" {
			*field.dst = value
		}
	}
	return e, nil
}

func joinHeader(items []any) (string, bool) {
	var value strings.Builder
	for i, item := range items {
		text, ok := item.(string)
		if !ok {
			return "", false
		}
		if i != 0 {
			value.WriteString(", ")
		}
		value.WriteString(text)
	}
	return value.String(), true
}

func jsonScalar(value any) string {
	switch x := value.(type) {
	case string:
		return x
	case json.Number:
		return string(x)
	default:
		return ""
	}
}
