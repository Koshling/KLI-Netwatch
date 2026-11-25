package event

import (
	"strings"
	"testing"
	"time"
)

func TestAccessLogFormats(t *testing.T) {
	const caddy = `{"request":{"client_ip":"2001:db8::1","remote_ip":"192.0.2.2","method":"GET","uri":"/check?q=1","proto":"HTTP/2.0","headers":{"User-Agent":["netwatch","smoke"],"X-Forwarded-For":["203.0.113.7","198.51.100.2"]}},"status":204,"size":12}`
	for _, tc := range []struct {
		name, format, line, ip, agent string
	}{
		{"caddy json", "json", `{"ts":1795608000.123456789,` + caddy[1:], "2001:db8::1", "netwatch, smoke"},
		{"caddy console", "caddy-console", "2026/11/25 12:00:00.123\tINFO\thttp.log.access.log0\thandled request\t" + caddy,
			"2001:db8::1", "netwatch, smoke"},
		{"traefik common", "traefik", `2001:db8::1 - - [25/Nov/2026:12:00:00 +0000] "GET /check?q=1 HTTP/2.0" 204 12 "-" "netwatch \"smoke\"" 1 "route@file" "http://127.0.0.1:8080" 2ms`,
			"2001:db8::1", `netwatch \"smoke\"`},
		{"traefik genericCLF", "combined", `2001:db8::1 - - [25/Nov/2026:12:00:00 +0000] "GET /check?q=1 HTTP/2.0" 204 12 "-" "netwatch"`,
			"2001:db8::1", "netwatch"},
		{"traefik json", "json", `{"StartUTC":"2026-11-25T12:00:00.123456789Z","time":"2026-11-25T12:00:05Z","ClientAddr":"[2001:db8::1]:12345","RequestMethod":"GET","RequestPath":"/check?q=1","RequestProtocol":"HTTP/2.0","DownstreamStatus":204,"OriginStatus":200,"DownstreamContentSize":12,"request_User-Agent":"netwatch"}`,
			"2001:db8::1", "netwatch"},
		{"apache vhost", "apache-vhost", `example.test:80 2001:db8::1 - - [25/Nov/2026:12:00:00 +0000] "GET /check?q=1 HTTP/2.0" 204 12 "-" "netwatch \"smoke\""`,
			"2001:db8::1", `netwatch \"smoke\"`},
		{"envoy default", "envoy", `[2026-11-25T12:00:00.000Z] "GET /check?q=1 HTTP/2" 204 - 0 12 2 - "2001:db8::1, 198.51.100.2" "netwatch "smoke"" "id" "example.test" "127.0.0.1:8080"`,
			"2001:db8::1", `netwatch "smoke"`},
		{"envoy without forwarded address", "envoy", `[2026-11-25T12:00:00.000Z] "GET /check?q=1 HTTP/2" 204 - 0 12 2 - "-" "netwatch" "id" "example.test" "127.0.0.1:8080"`,
			"", "netwatch"},
		{"envoy json", "json", `{"start_time":"2026-11-25T12:00:00Z","downstream_remote_address":"[2001:db8::1]:12345","request":"GET /check?q=1 HTTP/2","response_code":"204","bytes_sent":"12","user_agent":"netwatch"}`,
			"2001:db8::1", "netwatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := ParseAccess(tc.line, tc.format)
			if err != nil {
				t.Fatal(err)
			}
			if e.Status != 204 || e.ResponseSize != 12 || e.ClientIP != tc.ip ||
				e.Method != "GET" || e.URL != "/check?q=1" || !strings.HasPrefix(e.Protocol, "HTTP/2") ||
				e.UserAgent != tc.agent || e.Time.Unix() != 1795608000 {
				t.Fatalf("unexpected access event: %+v", e)
			}
			if (tc.name == "caddy json" || tc.name == "traefik json") && e.Time.Nanosecond() != 123456789 {
				t.Errorf("timestamp lost precision: %s", e.Time.Format(time.RFC3339Nano))
			}
		})
	}
}

func TestJSONAccessFields(t *testing.T) {
	p, err := NewAccessParser("json", "", "", map[string]string{
		"time": "/event/time~1ms", "client_ip": "/peer~0addr", "status": "/responses/0/code",
		"url": "/request/target",
	})
	if err != nil {
		t.Fatal(err)
	}
	line := `{"event":{"time/ms":1795608000123.456},"peer~addr":"192.0.2.33:3000","responses":[{"code":"200"}],"request":{"target":"/custom"},"status":404}`
	e, err := p(line)
	if err != nil || e.ClientIP != "192.0.2.33" || e.Status != 200 || e.URL != "/custom" ||
		e.Time.Unix() != 1795608000 || e.Time.Nanosecond() != 123456000 {
		t.Fatalf("%+v, %v", e, err)
	}
	for _, invalid := range []string{line + " {}", strings.Replace(line, `"200"`, `"bogus"`, 1), "null"} {
		if _, err := p(invalid); err == nil {
			t.Errorf("accepted %q", invalid)
		}
	}
	for _, path := range []string{"key", "/a~bad", "/a~"} {
		if _, err := NewAccessParser("json", "", "", map[string]string{"time": path}); err == nil {
			t.Errorf("accepted invalid pointer %q", path)
		}
	}
}

func TestNginxLogFormat(t *testing.T) {
	for _, tc := range []struct {
		name, template, escape, line, url, agent string
		fields                                   map[string]string
	}{
		{
			name: "directive and default escaping",
			template: `log_format custom 'host=$host [${time_local}] client=$remote_addr '
				'status=$status request="$request" agent="$http_user_agent" bytes=$body_bytes_sent';`,
			line: `host=test [25/Nov/2026:12:00:00 +0000] client=2001:db8::1 status=204 request="GET /custom HTTP/1.1" agent="netwatch \x22q\x22 caf\xC3\xA9" bytes=12`,
			url:  "/custom", agent: `netwatch "q" café`,
		},
		{
			name: "arbitrary JSON keys",
			template: `log_format custom escape=json '{"ts":"$msec","ip":"$remote_addr",'
				'"code":$status,"request":"$request","agent":"$http_user_agent","len":$bytes_sent}';`,
			line: `{"ts":"1795608000.123","ip":"2001:db8::1","code":204,"request":"GET /custom HTTP/1.1","agent":"netwatch \"q\" \\ café","len":12}`,
			url:  "/custom", agent: `netwatch "q" \ café`,
		},
		{
			name: "raw values and joined query", escape: "none",
			template: `$remote_addr|$time_iso8601|$status|$request_method|${uri}${is_args}${args}|$bytes_sent|$http_user_agent`,
			line:     `2001:db8::1|2026-11-25T12:00:00+00:00|204|GET|/custom?a=b|12|netwatch "q"`,
			url:      "/custom?a=b", agent: `netwatch "q"`,
		},
		{
			name:     "custom variables",
			template: `$ip $logged_at code=$code "$request" $sent "$http_user_agent"`,
			line:     `2001:db8::1 1795608000.123 code=204 "GET /custom HTTP/1.1" 12 "-"`,
			fields:   map[string]string{"client_ip": "$ip", "time": "${logged_at}", "status": "code", "response_size": "sent"},
			url:      "/custom",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := NewAccessParser("nginx", tc.template, tc.escape, tc.fields)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				e, err := p(tc.line)
				if err != nil || e.ClientIP != "2001:db8::1" || e.Time.Unix() != 1795608000 ||
					e.Status != 204 || e.ResponseSize != 12 || e.Method != "GET" || e.URL != tc.url || e.UserAgent != tc.agent {
					t.Fatalf("%+v, %v", e, err)
				}
			}
			if _, err := p(tc.line[:4]); err == nil {
				t.Error("accepted truncated line")
			}
		})
	}
}

func TestInvalidNginxLogFormats(t *testing.T) {
	for _, template := range []string{
		"", `$remote_addr $status`, `$msec $status`, `$remote_addr$msec $status`,
		`$${remote_addr} $msec`, `$remote_addr ${msec`,
		"log_format test 'foo $remote_addr\\n$msec';", "log_format test '$remote_addr $msec'",
	} {
		if _, err := NewAccessParser("nginx", template, "", nil); err == nil {
			t.Errorf("accepted invalid template %q", template)
		}
	}
}
