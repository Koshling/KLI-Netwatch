package geoip

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"sync/atomic"
	"testing"
)

func TestSystemDNSFallback(t *testing.T) {
	for _, firstErr := range []error{nil, errors.New("connection refused"), &net.DNSError{IsTimeout: true}} {
		var used atomic.Bool
		var destinations []string
		client, server := net.Pipe()
		dial := systemDNSFallback(
			func(_ context.Context, _, address string) (net.Conn, error) {
				destinations = append(destinations, address)
				if address == "download.db-ip.com:443" && firstErr != nil {
					return nil, firstErr
				}
				return client, nil
			},
			func(_ context.Context, host string) ([]netip.Addr, error) {
				if host != "download.db-ip.com" {
					t.Fatalf("unexpected lookup hostname %q", host)
				}
				return []netip.Addr{netip.MustParseAddr("2001:db8::1")}, nil
			},
			&used,
		)
		conn, err := dial(context.Background(), "tcp", "download.db-ip.com:443")
		client.Close()
		server.Close()
		want := []string{"download.db-ip.com:443"}
		var dnsErr *net.DNSError
		if errors.As(firstErr, &dnsErr) {
			want = append(want, "[2001:db8::1]:443")
			if conn != client || err != nil || !used.Load() {
				t.Fatalf("fallback did not succeed: conn=%v err=%v used=%v", conn, err, used.Load())
			}
		} else if err != firstErr || used.Load() {
			t.Fatalf("unexpected fallback: err=%v used=%v", err, used.Load())
		}
		if !slices.Equal(destinations, want) {
			t.Fatalf("dialed %v, want %v", destinations, want)
		}
	}
}
