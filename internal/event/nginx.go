package event

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type nginxToken struct {
	literal  string
	variable string
}

var nginxVariable = regexp.MustCompile(`^\$(?:([A-Za-z0-9_]+)|\{([A-Za-z0-9_]+)\})`)

var nginxFields = map[string][]string{
	"time":            {"time_iso8601", "msec", "time_local"},
	"client_ip":       {"remote_addr", "realip_remote_addr", "proxy_protocol_addr"},
	"request":         {"request"},
	"method":          {"request_method"},
	"url":             {"request_uri", "uri$is_args$args", "uri?$args", "uri", "document_uri"},
	"protocol":        {"server_protocol", "protocol"},
	"status":          {"status"},
	"response_size":   {"body_bytes_sent", "bytes_sent"},
	"referer":         {"http_referer"},
	"user_agent":      {"http_user_agent"},
	"x_forwarded_for": {"http_x_forwarded_for", "proxy_add_x_forwarded_for"},
}

func newNginxParser(template, escape string, fields map[string]string) (AccessParser, error) {
	if template == "" || len(template) > 8192 {
		return nil, fmt.Errorf("nginx_log_format must contain 1–8192 bytes")
	}
	if directive := strings.TrimLeft(template, " \t\n\r"); strings.HasPrefix(directive, "log_format") &&
		len(directive) > len("log_format") && strings.ContainsRune(" \t\r\n", rune(directive[len("log_format")])) {
		var err error
		template, escape, err = splitNginxDirective(directive, escape)
		if err != nil {
			return nil, err
		}
	}
	if escape == "" {
		escape = "default"
	}
	if escape != "default" && escape != "json" && escape != "none" {
		return nil, fmt.Errorf("nginx_escape must be default, json or none")
	}
	if strings.ContainsAny(template, "\r\n\x00") {
		return nil, fmt.Errorf("nginx_log_format must write one record per line")
	}

	tokens, err := tokenizeNginx(template)
	if err != nil {
		return nil, err
	}
	slots := make(map[string][]int)
	for name, variables := range nginxFields {
		if source, ok := fields[name]; ok {
			source = strings.TrimPrefix(source, "$")
			if strings.HasPrefix(source, "{") && strings.HasSuffix(source, "}") {
				source = source[1 : len(source)-1]
			}
			variables = []string{source}
		}
		for _, variable := range variables {
			for i, token := range tokens {
				if token.variable == variable {
					slots[name] = append(slots[name], i)
				}
			}
		}
		if fields[name] != "" && len(slots[name]) == 0 {
			return nil, fmt.Errorf("access_fields.%s references a variable missing from nginx_log_format", name)
		}
	}
	if len(slots["time"]) == 0 || len(slots["client_ip"]) == 0 {
		return nil, fmt.Errorf("nginx_log_format requires timestamp and client IP variables")
	}

	// Leave unused variables out of the capture list.
	group := make(map[int]int)
	for _, indexes := range slots {
		for _, i := range indexes {
			group[i] = 0
		}
	}
	var pattern strings.Builder
	pattern.WriteByte('^')
	n := 0
	for i, token := range tokens {
		if token.variable == "" {
			pattern.WriteString(regexp.QuoteMeta(token.literal))
			continue
		}
		body := nginxVariablePattern(tokens, i, escape)
		if _, used := group[i]; used {
			n++
			group[i] = n
			pattern.WriteString("(" + body + ")")
		} else {
			pattern.WriteString("(?:" + body + ")")
		}
	}
	pattern.WriteByte('$')
	match, err := regexp.Compile(pattern.String())
	if err != nil {
		return nil, fmt.Errorf("cannot compile nginx_log_format: %w", err)
	}
	return func(line string) (AccessEvent, error) {
		values := match.FindStringSubmatch(line)
		if values == nil {
			return AccessEvent{}, fmt.Errorf("line does not match nginx_log_format")
		}
		get := func(field string) string {
			for _, i := range slots[field] {
				if value := values[group[i]]; value != "" && value != "-" {
					return unescapeNginx(value, escape)
				}
			}
			return ""
		}
		e := AccessEvent{
			ClientIP: parseAddress(get("client_ip")), Referer: get("referer"),
			UserAgent: get("user_agent"), XForwardedFor: get("x_forwarded_for"),
		}
		if e.ClientIP == "" {
			return AccessEvent{}, fmt.Errorf("invalid client IP")
		}
		var err error
		e.Time, err = parseAccessTime(get("time"))
		if err != nil {
			return AccessEvent{}, err
		}
		if status := get("status"); status != "" {
			e.Status, err = loggedStatus(status)
			if err != nil {
				return AccessEvent{}, err
			}
		}
		if size := get("response_size"); size != "" {
			e.ResponseSize, err = decimalField(size, "response size", 63)
			if err != nil {
				return AccessEvent{}, err
			}
		}
		e.Method, e.URL, e.Protocol = parseAccessRequest(get("request"))
		if method := get("method"); method != "" {
			e.Method = method
		}
		if url := get("url"); url != "" {
			e.URL = url
		}
		if protocol := get("protocol"); protocol != "" {
			e.Protocol = protocol
		}
		return e, nil
	}, nil
}

func tokenizeNginx(template string) ([]nginxToken, error) {
	var tokens []nginxToken
	for template != "" {
		i := strings.IndexByte(template, '$')
		if i < 0 {
			tokens = append(tokens, nginxToken{literal: template})
			break
		}
		if i > 0 {
			tokens = append(tokens, nginxToken{literal: template[:i]})
		}
		m := nginxVariable.FindStringSubmatch(template[i:])
		if m == nil {
			return nil, fmt.Errorf("invalid variable in nginx_log_format")
		}
		tokens = append(tokens, nginxToken{variable: m[1] + m[2]})
		template = template[i+len(m[0]):]
	}

	// Nginx commonly writes a URI and its query as one value using this trio.
	result := tokens[:0]
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if token.variable == "uri" && i+2 < len(tokens) && tokens[i+2].variable == "args" {
			switch {
			case tokens[i+1].variable == "is_args":
				token.variable = "uri$is_args$args"
				i += 2
			case tokens[i+1].literal == "?":
				token.variable = "uri?$args"
				i += 2
			}
		}
		if token.variable != "" && len(result) > 0 && result[len(result)-1].variable != "" {
			return nil, fmt.Errorf("adjacent nginx variables require a literal separator")
		}
		result = append(result, token)
	}
	return result, nil
}

func nginxVariablePattern(tokens []nginxToken, i int, escape string) string {
	if i > 0 && i+1 < len(tokens) {
		before, after := tokens[i-1].literal, tokens[i+1].literal
		if strings.HasSuffix(before, `"`) && strings.HasPrefix(after, `"`) {
			switch escape {
			case "json":
				return `(?:[^"\\]|\\.)*`
			case "default":
				return `[^"]*`
			}
		}
	}
	switch tokens[i].variable {
	case "status":
		return `[0-9]{3}|-`
	case "body_bytes_sent", "bytes_sent", "remote_port":
		return `[0-9]+|-`
	case "time_local":
		return `[0-9]{2}/[A-Za-z]{3}/[0-9]{4}:[0-9]{2}:[0-9]{2}:[0-9]{2} [+-][0-9]{4}|-`
	case "time_iso8601", "msec", "request_method", "server_protocol", "protocol":
		return `[^ \t]*?`
	case "remote_addr", "realip_remote_addr", "proxy_protocol_addr":
		return `[A-Fa-f0-9.:\[\]]+|-`
	}
	return `.*?`
}

func unescapeNginx(value, escape string) string {
	switch escape {
	case "json":
		if strings.Contains(value, "\\") {
			var decoded string
			if json.Unmarshal([]byte(`"`+value+`"`), &decoded) == nil {
				return decoded
			}
		}
	case "default":
		if strings.Contains(value, `\x`) {
			var decoded strings.Builder
			for i := 0; i < len(value); i++ {
				if value[i] == '\\' && i+3 < len(value) && value[i+1] == 'x' {
					n, err := strconv.ParseUint(value[i+2:i+4], 16, 8)
					if err == nil {
						decoded.WriteByte(byte(n))
						i += 3
						continue
					}
				}
				decoded.WriteByte(value[i])
			}
			return decoded.String()
		}
	}
	return value
}

// splitNginxDirective accepts one complete directive with its template fragments.
func splitNginxDirective(directive, escape string) (string, string, error) {
	rest := strings.TrimSpace(directive[len("log_format"):])
	end := strings.IndexAny(rest, " \t\r\n")
	if end <= 0 {
		return "", "", fmt.Errorf("expected a log_format name followed by the template")
	}
	rest = strings.TrimSpace(rest[end:])
	if strings.HasPrefix(rest, "escape=") {
		end = strings.IndexAny(rest, " \t\r\n")
		if end < 0 {
			return "", "", fmt.Errorf("missing log_format template after escape option")
		}
		mode := strings.TrimPrefix(rest[:end], "escape=")
		if mode == "" || (escape != "" && mode != escape) {
			return "", "", fmt.Errorf("invalid or conflicting log_format escape option")
		}
		escape, rest = mode, strings.TrimSpace(rest[end:])
	}

	var template strings.Builder
	for {
		switch {
		case strings.HasPrefix(rest, ";"):
			if suffix := strings.TrimSpace(rest[1:]); suffix != "" && !strings.HasPrefix(suffix, "#") {
				return "", "", fmt.Errorf("expected a single log_format directive")
			}
			return template.String(), escape, nil
		case strings.HasPrefix(rest, "#"):
			_, rest, _ = strings.Cut(rest, "\n")
			rest = strings.TrimSpace(rest)
			continue
		case rest == "":
			return "", "", fmt.Errorf("log_format directive requires a final semicolon")
		}
		var quote byte
		i := 0
		if rest[0] == '\'' || rest[0] == '"' {
			quote, i = rest[0], 1
		}
		for ; i < len(rest); i++ {
			b := rest[i]
			if (quote != 0 && b == quote) || (quote == 0 && strings.ContainsRune(" \t\r\n;", rune(b))) {
				break
			}
			if b == '\\' && i+1 < len(rest) {
				switch rest[i+1] {
				case '\\', '\'', '"':
					i++
					b = rest[i]
				case 't':
					i++
					b = '\t'
				case 'r', 'n':
					return "", "", fmt.Errorf("nginx_log_format must write one record per line")
				}
			}
			template.WriteByte(b)
		}
		if quote != 0 {
			if i == len(rest) {
				return "", "", fmt.Errorf("unterminated log_format fragment")
			}
			i++
		}
		rest = strings.TrimSpace(rest[i:])
	}
}
