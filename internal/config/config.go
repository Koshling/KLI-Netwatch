package config

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"time"
)

type Config struct {
	AccessLog            string
	ErrorLog             string
	AccessFormat         string
	MaxLineSize          int
	PollInterval         time.Duration
	GeoIPIPv4DB          string
	GeoIPIPv6DB          string
	GeoIPPrompt          bool
	GeoIPSource          string
	ServerLatitude       float64
	ServerLongitude      float64
	ServerCoordinatesSet bool
}

type configFile struct {
	AccessLog       string   `json:"access_log"`
	ErrorLog        string   `json:"error_log"`
	AccessFormat    string   `json:"access_format"`
	MaxLineSize     int      `json:"max_line_size"`
	PollInterval    string   `json:"poll_interval"`
	GeoIPIPv4DB     string   `json:"geoip_ipv4_db"`
	GeoIPIPv6DB     string   `json:"geoip_ipv6_db"`
	GeoIPPrompt     bool     `json:"geoip_prompt,omitempty"`
	GeoIPSource     string   `json:"geoip_source,omitempty"`
	ServerLatitude  *float64 `json:"server_latitude"`
	ServerLongitude *float64 `json:"server_longitude"`
}

func Default() Config {
	return Config{
		AccessLog:    "/var/log/nginx/access.log",
		ErrorLog:     "/var/log/nginx/error.log",
		AccessFormat: "combined",
		MaxLineSize:  65536,
		PollInterval: 500 * time.Millisecond,
	}
}

func Load(path string) (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var file configFile
	if err := json.Unmarshal(data, &file); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	if file.AccessLog != "" {
		cfg.AccessLog = file.AccessLog
	}
	if file.ErrorLog != "" {
		cfg.ErrorLog = file.ErrorLog
	}
	if file.AccessFormat != "" {
		cfg.AccessFormat = file.AccessFormat
	}
	if file.MaxLineSize != 0 {
		cfg.MaxLineSize = file.MaxLineSize
	}
	if file.PollInterval != "" {
		interval, err := time.ParseDuration(file.PollInterval)
		if err != nil {
			return Config{}, fmt.Errorf("invalid poll_interval: %w", err)
		}
		cfg.PollInterval = interval
	}

	// GeoIP database paths are optional. A single IP family is supported.
	cfg.GeoIPIPv4DB = file.GeoIPIPv4DB
	cfg.GeoIPIPv6DB = file.GeoIPIPv6DB
	cfg.GeoIPPrompt = file.GeoIPPrompt
	cfg.GeoIPSource = file.GeoIPSource

	// Pointers distinguish omitted coordinates from valid zero values.
	if (file.ServerLatitude == nil) != (file.ServerLongitude == nil) {
		return Config{}, fmt.Errorf("server_latitude and server_longitude must be configured together")
	}
	if file.ServerLatitude != nil {
		cfg.ServerLatitude = *file.ServerLatitude
		cfg.ServerLongitude = *file.ServerLongitude
		cfg.ServerCoordinatesSet = true
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if c.AccessLog == "" || c.ErrorLog == "" {
		return fmt.Errorf("log paths cannot be empty")
	}

	switch c.AccessFormat {
	case "combined", "common", "main", "json":
	default:
		return fmt.Errorf("unsupported access_format: %q", c.AccessFormat)
	}

	if c.MaxLineSize < 1024 || c.MaxLineSize > 1048576 {
		return fmt.Errorf("max_line_size must be between 1024 and 1048576")
	}

	if c.PollInterval < 100*time.Millisecond ||
		c.PollInterval > 60*time.Second {
		return fmt.Errorf("poll_interval must be between 100ms and 60s")
	}

	if c.ServerCoordinatesSet {
		if math.IsNaN(c.ServerLatitude) || math.IsInf(c.ServerLatitude, 0) || c.ServerLatitude < -90 || c.ServerLatitude > 90 {
			return fmt.Errorf("server_latitude must be finite and between -90 and 90")
		}
		if math.IsNaN(c.ServerLongitude) || math.IsInf(c.ServerLongitude, 0) || c.ServerLongitude < -180 || c.ServerLongitude > 180 {
			return fmt.Errorf("server_longitude must be finite and between -180 and 180")
		}
	}

	return nil
}
