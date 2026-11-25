package config

import (
	"math"
	"netwatch/internal/event"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadTestJSON(t *testing.T, body string) (Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "netwatch.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestLoadGeoIPPathsAndCoordinates(t *testing.T) {
	cfg, err := loadTestJSON(t, `{"geoip_ipv4_db":"/srv/geo/v4.csv","geoip_ipv6_db":"/srv/geo/v6.csv","server_latitude":0,"server_longitude":0}`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GeoIPIPv4DB != "/srv/geo/v4.csv" || cfg.GeoIPIPv6DB != "/srv/geo/v6.csv" {
		t.Fatalf("database paths lost: %+v", cfg)
	}
	if !cfg.ServerCoordinatesSet || cfg.ServerLatitude != 0 || cfg.ServerLongitude != 0 {
		t.Fatalf("zero coordinates lost: %+v", cfg)
	}
}

func TestLoadGeoIPOptional(t *testing.T) {
	cfg, err := loadTestJSON(t, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerCoordinatesSet || cfg.GeoIPIPv4DB != "" || cfg.GeoIPIPv6DB != "" {
		t.Fatalf("unexpected GeoIP configuration: %+v", cfg)
	}
}

func TestLoadSingleIPFamily(t *testing.T) {
	cfg, err := loadTestJSON(t, `{"geoip_ipv4_db":"/srv/geo/v4.csv"}`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GeoIPIPv4DB != "/srv/geo/v4.csv" || cfg.GeoIPIPv6DB != "" {
		t.Fatalf("single family lost: %+v", cfg)
	}
}

func TestLoadGeoIPCoordinateValidation(t *testing.T) {
	cases := []struct{ name, json, want string }{
		{"missing longitude", `{"server_latitude":52}`, "must be configured together"},
		{"missing latitude", `{"server_longitude":13}`, "must be configured together"},
		{"latitude too high", `{"server_latitude":91,"server_longitude":13}`, "server_latitude"},
		{"latitude too low", `{"server_latitude":-91,"server_longitude":13}`, "server_latitude"},
		{"longitude too high", `{"server_latitude":52,"server_longitude":181}`, "server_longitude"},
		{"longitude too low", `{"server_latitude":52,"server_longitude":-181}`, "server_longitude"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadTestJSON(t, tc.json)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v; want %q", err, tc.want)
			}
		})
	}
}

func TestValidateRejectsNonFiniteServerCoordinates(t *testing.T) {
	// JSON cannot express NaN or Inf; check programmatically constructed configs separately.
	cfg := Default()
	cfg.ServerCoordinatesSet = true
	cfg.ServerLatitude = math.NaN()
	if err := cfg.Validate(); err == nil {
		t.Fatal("NaN latitude accepted")
	}
	cfg.ServerLatitude = 0
	cfg.ServerLongitude = math.Inf(1)
	if err := cfg.Validate(); err == nil {
		t.Fatal("Inf longitude accepted")
	}
}

func TestTrafficFilterConfig(t *testing.T) {
	monitor := event.AccessEvent{Method: "GET", URL: "/health", Status: 200, UserAgent: "Uptime-Kuma/2.0"}
	hidden := event.AccessEvent{ClientIP: "2001:db8::42", Status: 500}
	for _, tc := range []struct {
		name, json                     string
		apply, showMonitor, showHidden bool
	}{
		{"old config", `{}`, true, false, true},
		{"implicit apply", `{"hidden_ips":["2001:db8::42"]}`, true, false, false},
		{"explicit apply", `{"apply_traffic_filters":true,"hidden_ips":["2001:db8::42"]}`, true, false, false},
		{"null uses default", `{"apply_traffic_filters":null,"hidden_ips":["2001:db8::42"]}`, true, false, false},
		{"monitor override", `{"show_monitoring_bot_traffic":true,"hidden_ips":["2001:db8::42"]}`, true, true, false},
		{"master off", `{"apply_traffic_filters":false,"hidden_ips":["2001:db8::42","invalid"]}`, false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadTestJSON(t, tc.json)
			if err != nil {
				t.Fatal(err)
			}
			keep, err := cfg.TrafficFilter()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ApplyTrafficFilters != tc.apply || keep(monitor) != tc.showMonitor || keep(hidden) != tc.showHidden {
				t.Fatalf("apply=%v, monitoring visible=%v, hidden IP visible=%v", cfg.ApplyTrafficFilters, keep(monitor), keep(hidden))
			}
		})
	}
	if _, err := loadTestJSON(t, `{"hidden_ips":["invalid"]}`); err == nil || !strings.Contains(err.Error(), "hidden_ips") {
		t.Fatalf("expected invalid hidden IP error, got %v", err)
	}
}
