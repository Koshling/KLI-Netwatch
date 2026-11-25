package event

import "testing"

func TestTrafficFilter(t *testing.T) {
	banned := []string{"192.0.2.42", "2001:db8::42"}
	keep, err := NewTrafficFilter(false, banned)
	if err != nil {
		t.Fatal(err)
	}
	show, err := NewTrafficFilter(true, banned)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ip, method, agent string
		status            int
		visible, banned   bool
	}{
		{"", "GET", "Uptime-Kuma/2.1", 200, false, false},
		{"", "HEAD", "Prometheus/3.7", 204, false, false},
		{"", "GET", "kube-probe/1.37", 200, false, false},
		{"", "GET", "ELB-HealthChecker/2.0", 200, false, false},
		{"", "HEAD", "Mozilla/5.0+(compatible; UptimeRobot/2.0; http://www.uptimerobot.com/)", 200, false, false},
		{"", "GET", "Uptime-Kuma/2.1", 302, true, false},
		{"", "GET", "Prometheus/3.7", 403, true, false},
		{"", "GET", "Prometheus/3.7", 503, true, false},
		{"", "GET", "Prometheus/3.7", 0, true, false},
		{"", "POST", "Uptime-Kuma/2.1", 200, true, false},
		{"", "", "Uptime-Kuma/2.1", 200, true, false},
		{"", "GET", "Mozilla/5.0", 200, true, false},
		{"", "GET", "Nikto/2.5 (Prometheus/3.7)", 200, true, false},
		{"", "GET", "Prometheus/3.7 Nikto/2.5", 200, true, false},
		{"", "GET", "Prometheus-Proxy/3.7", 200, true, false},
		{"", "GET", "-", 200, true, false},
		{"192.0.2.42", "GET", "Mozilla/5.0", 200, false, true},
		{"::ffff:192.0.2.42", "GET", "Mozilla/5.0", 404, false, true},
		{"2001:0db8::42", "GET", "Uptime-Kuma/2.1", 503, false, true},
		{"2001:db8::43", "GET", "Mozilla/5.0", 200, true, false},
	} {
		e := AccessEvent{ClientIP: tc.ip, Method: tc.method, UserAgent: tc.agent, Status: tc.status}
		if got := keep(e); got != tc.visible {
			t.Errorf("%+v: got visible %v, want %v", e, got, tc.visible)
		}
		if got := show(e); got == tc.banned {
			t.Errorf("%+v: got visible %v with monitoring enabled, banned %v", e, got, tc.banned)
		}
	}
	for _, invalid := range []string{"192.0.2.42:80", "192.0.2.0/24", "fe80::1%eth0", "localhost"} {
		if _, err := NewTrafficFilter(false, []string{invalid}); err == nil {
			t.Errorf("accepted %q in banned_ips", invalid)
		}
	}
}
