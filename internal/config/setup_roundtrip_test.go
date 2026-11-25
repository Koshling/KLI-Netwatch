package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSetupConfigRoundTrip(t *testing.T) {
	for _, format := range []string{"nginx", "json"} {
		t.Run(format, func(t *testing.T) {
			cfg := Default()
			cfg.AccessFormat, cfg.GeoIPPrompt = format, true
			cfg.MaxLineSize, cfg.PollInterval = 4096, 2*time.Second
			cfg.ServerCoordinatesSet, cfg.ServerLongitude = true, 37.62
			if format == "nginx" {
				cfg.NginxLogFormat, cfg.NginxEscape = "$msec $logged_client $status", "none"
				cfg.AccessFields = map[string]string{"client_ip": "logged_client"}
			} else {
				cfg.AccessFields = map[string]string{"client_ip": "/peer/ip"}
			}
			path := filepath.Join(t.TempDir(), "netwatch.json")
			if err := CreateSetup(path, cfg); err != nil {
				t.Fatal(err)
			}
			check := func() {
				t.Helper()
				got, err := Load(path)
				if err != nil || !reflect.DeepEqual(got, cfg) {
					t.Fatalf("got %+v, %v; want %+v", got, err, cfg)
				}
			}
			check()

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			fields["external_setting"] = json.RawMessage(`{"keep":true}`)
			data, err = json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			cfg.GeoIPPrompt = false
			if err := SaveGeoSetup(path, cfg); err != nil {
				t.Fatal(err)
			}
			check()
			data, err = os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var updated map[string]json.RawMessage
			if err := json.Unmarshal(data, &updated); err != nil || !json.Valid(updated["external_setting"]) {
				t.Fatalf("external setting lost: %s, %v", data, err)
			}
		})
	}
}
