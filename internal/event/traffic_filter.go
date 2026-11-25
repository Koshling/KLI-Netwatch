package event

import (
	"fmt"
	"net/netip"
	"strings"
)

// TrafficFilter decides which parsed requests reach the monitor in every mode.
type TrafficFilter func(AccessEvent) bool

func NewTrafficFilter(apply, showMonitoring bool, hiddenIPs []string) (TrafficFilter, error) {
	if !apply {
		return func(AccessEvent) bool { return true }, nil
	}
	hidden := make(map[netip.Addr]struct{}, len(hiddenIPs))
	for _, value := range hiddenIPs {
		ip, err := netip.ParseAddr(value)
		if err != nil || ip.Zone() != "" {
			return nil, fmt.Errorf("invalid IP address %q in hidden_ips", value)
		}
		hidden[ip.Unmap()] = struct{}{}
	}
	return func(e AccessEvent) bool {
		if len(hidden) > 0 {
			if ip, err := netip.ParseAddr(e.ClientIP); err == nil {
				if _, blocked := hidden[ip.Unmap()]; blocked {
					return false
				}
			}
		}

		// A failed check or an unexpected method remains visible.
		if showMonitoring || e.Status < 200 || e.Status >= 300 ||
			(e.Method != "GET" && e.Method != "HEAD") {
			return true
		}
		return !monitoringAgent(e.UserAgent)
	}, nil
}

func monitoringAgent(agent string) bool {
	agent = strings.TrimSpace(agent)
	if strings.EqualFold(agent, "Mozilla/5.0+(compatible; UptimeRobot/2.0; http://www.uptimerobot.com/)") {
		return true
	}
	// Identify a standalone product token independently of its version.
	if strings.ContainsAny(agent, " \t,") {
		return false
	}
	product, _, _ := strings.Cut(agent, "/")
	for _, name := range [...]string{"Uptime-Kuma", "Prometheus", "kube-probe", "ELB-HealthChecker"} {
		if strings.EqualFold(product, name) {
			return true
		}
	}
	return false
}
