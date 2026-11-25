package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Find returns the existing configuration, or the suggested user path.
func Find() (path string, exists bool, err error) {
	user, userErr := userConfigPath()
	dirs := os.Getenv("XDG_CONFIG_DIRS")
	if dirs == "" {
		dirs = "/etc/xdg"
	}
	candidates := []string{user, filepath.Join("configs", "netwatch.json")}
	for _, dir := range filepath.SplitList(dirs) {
		if filepath.IsAbs(dir) {
			candidates = append(candidates, filepath.Join(dir, "netwatch", "netwatch.json"))
		}
	}
	candidates = append(candidates, "/etc/netwatch/netwatch.json")
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, err := os.Lstat(candidate); err == nil {
			return candidate, true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
	}
	return user, false, userErr
}

func userConfigPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(dir) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "netwatch", "netwatch.json"), nil
}

func GeoDataDir() (string, error) {
	dir := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(dir) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dir, "netwatch"), nil
}

func CreateSetup(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	file := configFile{
		AccessLog: cfg.AccessLog, ErrorLog: cfg.ErrorLog, AccessFormat: cfg.AccessFormat,
		NginxLogFormat: cfg.NginxLogFormat, NginxEscape: cfg.NginxEscape, AccessFields: cfg.AccessFields,
		GeoIPIPv4DB: cfg.GeoIPIPv4DB, GeoIPIPv6DB: cfg.GeoIPIPv6DB,
		GeoIPPrompt: cfg.GeoIPPrompt, GeoIPSource: cfg.GeoIPSource,
		MaxLineSize: cfg.MaxLineSize, PollInterval: cfg.PollInterval.String(),
	}
	if cfg.ServerCoordinatesSet {
		file.ServerLatitude, file.ServerLongitude = &cfg.ServerLatitude, &cfg.ServerLongitude
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return writeSetup(path, file, true)
}

// SaveGeoSetup preserves the current log settings and any unrelated JSON fields.
func SaveGeoSetup(path string, cfg Config) error {
	// Resolve a symlink so an existing config link remains intact.
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := syscall.Flock(int(dir.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}
	var current configFile
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	if !current.GeoIPPrompt || current.GeoIPIPv4DB != "" || current.GeoIPIPv6DB != "" {
		return fmt.Errorf("GeoIP settings changed during setup; reopen %s", path)
	}
	for key, value := range map[string]any{
		"geoip_ipv4_db": cfg.GeoIPIPv4DB, "geoip_ipv6_db": cfg.GeoIPIPv6DB,
		"geoip_prompt": cfg.GeoIPPrompt, "geoip_source": cfg.GeoIPSource,
	} {
		file[key], _ = json.Marshal(value)
	}
	return writeSetup(path, file, false)
}

func writeSetup(path string, value any, create bool) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".netwatch-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if create {
		return os.Link(tmp.Name(), path)
	}
	return os.Rename(tmp.Name(), path)
}
