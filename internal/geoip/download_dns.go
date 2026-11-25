package geoip

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

func dbipTransport(fallbackUsed *atomic.Bool) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxResponseHeaderBytes = 64 << 10
	transport.DialContext = systemDNSFallback(transport.DialContext, systemDNSAddresses, fallbackUsed)
	return transport
}

func systemDNSFallback(
	dial func(context.Context, string, string) (net.Conn, error),
	lookup func(context.Context, string) ([]netip.Addr, error),
	fallbackUsed *atomic.Bool,
) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dial(ctx, network, address)
		var dnsErr *net.DNSError
		if !errors.As(err, &dnsErr) || ctx.Err() != nil {
			return conn, err
		}
		conn, fallbackErr := dialWithSystemDNS(ctx, network, address, dial, lookup)
		if fallbackErr != nil {
			return nil, fmt.Errorf("%w (system DNS fallback: %v)", err, fallbackErr)
		}
		fallbackUsed.Store(true)
		return conn, nil
	}
}

func dialWithSystemDNS(
	ctx context.Context, network, address string,
	dial func(context.Context, string, string) (net.Conn, error),
	lookup func(context.Context, string) ([]netip.Addr, error),
) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	addresses, err := lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addresses) == 0 {
		return nil, errors.New("no IP addresses returned")
	}
	// Only the dial address changes. HTTP and TLS still use the original hostname.
	for _, ip := range addresses {
		attempt, stop := context.WithTimeout(ctx, 2*time.Second)
		conn, dialErr := dial(attempt, network, net.JoinHostPort(ip.String(), port))
		stop()
		if dialErr == nil {
			return conn, nil
		}
		err = dialErr
		if ctx.Err() != nil {
			break
		}
	}
	return nil, err
}

func systemDNSAddresses(ctx context.Context, host string) ([]netip.Addr, error) {
	// Use the system NSS configuration when the Go resolver fails. The
	// options delimiter keeps a configured host from becoming an argument.
	cmd := exec.CommandContext(ctx, "getent", "--", "ahosts", host)
	cmd.WaitDelay = time.Second
	var output bytes.Buffer
	remaining := int64(64 << 10)
	cmd.Stdout = boundedWriter{&output, &remaining, "system DNS output"}
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	var addresses []netip.Addr
	// getent emits a separate row per socket type. Keep just the TCP rows.
	for _, line := range strings.Split(output.String(), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] != "STREAM" {
			continue
		}
		if addr, err := netip.ParseAddr(fields[0]); err == nil {
			addresses = append(addresses, addr)
		}
	}
	return addresses, nil
}
