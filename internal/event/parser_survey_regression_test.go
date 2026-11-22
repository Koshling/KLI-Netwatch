package event

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParserSurveyRegression(t *testing.T) {
	cases := []struct{ name, format, line, reason, expected string }{
		{"json status='200'", "json", "{\"time\": \"2026-11-22T11:00:00+03:00\", \"remote_addr\": \"2001:db8::1\", \"method\": \"PATCH\", \"url\": \"/sample?q=1\", \"status\": \"200\", \"bytes_sent\": 12}", "", "{\"Status\": 200}"},
		{"json status=200", "json", "{\"time\": \"2026-11-22T11:00:00+03:00\", \"remote_addr\": \"2001:db8::1\", \"method\": \"PATCH\", \"url\": \"/sample?q=1\", \"status\": 200, \"bytes_sent\": 12}", "", "{\"Status\": 200}"},
		{"json status='abc'", "json", "{\"time\": \"2026-11-22T11:00:00+03:00\", \"remote_addr\": \"2001:db8::1\", \"method\": \"PATCH\", \"url\": \"/sample?q=1\", \"status\": \"abc\", \"bytes_sent\": 12}", "status", "null"},
		{"json status=None", "json", "{\"time\": \"2026-11-22T11:00:00+03:00\", \"remote_addr\": \"2001:db8::1\", \"method\": \"PATCH\", \"url\": \"/sample?q=1\", \"status\": null, \"bytes_sent\": 12}", "status", "null"},
		{"json bytes_sent='12'", "json", "{\"time\": \"2026-11-22T11:00:00+03:00\", \"remote_addr\": \"2001:db8::1\", \"method\": \"PATCH\", \"url\": \"/sample?q=1\", \"status\": 200, \"bytes_sent\": \"12\"}", "", "{\"ResponseSize\": 12}"},
		{"json bytes_sent='abc'", "json", "{\"time\": \"2026-11-22T11:00:00+03:00\", \"remote_addr\": \"2001:db8::1\", \"method\": \"PATCH\", \"url\": \"/sample?q=1\", \"status\": 200, \"bytes_sent\": \"abc\"}", "response size", "null"},
		{"json bytes_sent=-1", "json", "{\"time\": \"2026-11-22T11:00:00+03:00\", \"remote_addr\": \"2001:db8::1\", \"method\": \"PATCH\", \"url\": \"/sample?q=1\", \"status\": 200, \"bytes_sent\": -1}", "response size", "null"},
		{"json time=None", "json", "{\"time\": null, \"remote_addr\": \"2001:db8::1\", \"method\": \"PATCH\", \"url\": \"/sample?q=1\", \"status\": 200, \"bytes_sent\": 12}", "timestamp", "null"},
		{"json time='bad'", "json", "{\"time\": \"bad\", \"remote_addr\": \"2001:db8::1\", \"method\": \"PATCH\", \"url\": \"/sample?q=1\", \"status\": 200, \"bytes_sent\": 12}", "timestamp", "null"},
		{"json remote_addr='bad'", "json", "{\"time\": \"2026-11-22T11:00:00+03:00\", \"remote_addr\": \"bad\", \"method\": \"PATCH\", \"url\": \"/sample?q=1\", \"status\": 200, \"bytes_sent\": 12}", "client IP", "null"},
		{"json method=123", "json", "{\"time\": \"2026-11-22T11:00:00+03:00\", \"remote_addr\": \"2001:db8::1\", \"method\": 123, \"url\": \"/sample?q=1\", \"status\": 200, \"bytes_sent\": 12}", "method", "null"},
		{"common valid", "common", "2001:db8::1 - - [22/Nov/2026:11:00:00 +0300] \"PATCH /sample?q=1 HTTP/1.1\" 204 0", "", "{\"ClientIP\": \"2001:db8::1\", \"URL\": \"/sample?q=1\", \"Method\": \"PATCH\", \"Status\": 204, \"Time\": \"2026-11-22T11:00:00+03:00\"}"},
		{"common invalid IP", "common", "bad - - [22/Nov/2026:11:00:00 +0300] \"GET / HTTP/1.1\" 200 12", "client IP", "null"},
		{"common invalid size", "common", "192.0.2.1 - - [22/Nov/2026:11:00:00 +0300] \"GET / HTTP/1.1\" 200 abc", "response size", "null"},
		{"common negative size", "common", "192.0.2.1 - - [22/Nov/2026:11:00:00 +0300] \"GET / HTTP/1.1\" 200 -1", "response size", "null"},
		{"common invalid status", "common", "192.0.2.1 - - [22/Nov/2026:11:00:00 +0300] \"GET / HTTP/1.1\" 999 12", "status", "null"},
		{"common missing request", "common", "192.0.2.1 - - [22/Nov/2026:11:00:00 +0300] \"-\" 400 0", "", "{\"Status\": 400, \"Method\": \"\", \"URL\": \"\", \"ClientIP\": \"192.0.2.1\", \"Time\": \"2026-11-22T11:00:00+03:00\"}"},
		{"combined valid", "combined", "2001:db8::1 - - [22/Nov/2026:11:00:00 +0300] \"PATCH /sample?q=1 HTTP/1.1\" 204 0 \"-\" \"Agent\"", "", "{\"ClientIP\": \"2001:db8::1\", \"URL\": \"/sample?q=1\", \"Method\": \"PATCH\", \"Status\": 204, \"Time\": \"2026-11-22T11:00:00+03:00\"}"},
		{"combined invalid IP", "combined", "bad - - [22/Nov/2026:11:00:00 +0300] \"GET / HTTP/1.1\" 200 12 \"-\" \"Agent\"", "client IP", "null"},
		{"combined invalid size", "combined", "192.0.2.1 - - [22/Nov/2026:11:00:00 +0300] \"GET / HTTP/1.1\" 200 abc \"-\" \"Agent\"", "response size", "null"},
		{"combined negative size", "combined", "192.0.2.1 - - [22/Nov/2026:11:00:00 +0300] \"GET / HTTP/1.1\" 200 -1 \"-\" \"Agent\"", "response size", "null"},
		{"combined invalid status", "combined", "192.0.2.1 - - [22/Nov/2026:11:00:00 +0300] \"GET / HTTP/1.1\" 999 12 \"-\" \"Agent\"", "status", "null"},
		{"combined missing request", "combined", "192.0.2.1 - - [22/Nov/2026:11:00:00 +0300] \"-\" 400 0 \"-\" \"Agent\"", "", "{\"Status\": 400, \"Method\": \"\", \"URL\": \"\", \"ClientIP\": \"192.0.2.1\", \"Time\": \"2026-11-22T11:00:00+03:00\"}"},
		{"main valid", "main", "2001:db8::1 - - [22/Nov/2026:11:00:00 +0300] \"PATCH /sample?q=1 HTTP/1.1\" 204 0 \"-\" \"Agent\" \"198.51.100.2\"", "", "{\"ClientIP\": \"2001:db8::1\", \"URL\": \"/sample?q=1\", \"Method\": \"PATCH\", \"Status\": 204, \"Time\": \"2026-11-22T11:00:00+03:00\"}"},
		{"main invalid IP", "main", "bad - - [22/Nov/2026:11:00:00 +0300] \"GET / HTTP/1.1\" 200 12 \"-\" \"Agent\" \"198.51.100.2\"", "client IP", "null"},
		{"main invalid size", "main", "192.0.2.1 - - [22/Nov/2026:11:00:00 +0300] \"GET / HTTP/1.1\" 200 abc \"-\" \"Agent\" \"198.51.100.2\"", "response size", "null"},
		{"main negative size", "main", "192.0.2.1 - - [22/Nov/2026:11:00:00 +0300] \"GET / HTTP/1.1\" 200 -1 \"-\" \"Agent\" \"198.51.100.2\"", "response size", "null"},
		{"main invalid status", "main", "192.0.2.1 - - [22/Nov/2026:11:00:00 +0300] \"GET / HTTP/1.1\" 999 12 \"-\" \"Agent\" \"198.51.100.2\"", "status", "null"},
		{"main missing request", "main", "192.0.2.1 - - [22/Nov/2026:11:00:00 +0300] \"-\" 400 0 \"-\" \"Agent\" \"198.51.100.2\"", "", "{\"Status\": 400, \"Method\": \"\", \"URL\": \"\", \"ClientIP\": \"192.0.2.1\", \"Time\": \"2026-11-22T11:00:00+03:00\"}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e, err := ParseAccess(c.line, c.format)
			if c.reason != "" {
				if err == nil || !strings.Contains(err.Error(), c.reason) {
					t.Fatalf("expected %s, got %v", c.reason, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			var got, want map[string]any
			if err = json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal([]byte(c.expected), &want); err != nil {
				t.Fatal(err)
			}
			for key, value := range want {
				if !reflect.DeepEqual(got[key], value) {
					t.Errorf("%s: got %v want %v", key, got[key], value)
				}
			}
		})
	}
}
func TestStatusAndSizeBoundaries(t *testing.T) {
	for _, s := range []string{"+200", "0200", "099", "600"} {
		if _, err := accessStatus(s); err == nil {
			t.Errorf("accepted %s", s)
		}
	}
	if _, err := decimalField("9223372036854775808", "response size", 63); err == nil {
		t.Error("size overflow")
	}
}
