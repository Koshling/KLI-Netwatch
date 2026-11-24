package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"netwatch/internal/config"
	"netwatch/internal/event"
	"netwatch/internal/geoip"
)

type setupPrompts struct {
	lines *bufio.Scanner
	out   io.Writer
}

func (p setupPrompts) ask(label, fallback string) (string, error) {
	fmt.Fprintf(p.out, "%s [%s]: ", label, fallback)
	if !p.lines.Scan() {
		if err := p.lines.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}
	answer := strings.TrimSpace(p.lines.Text())
	if answer == "" {
		return fallback, nil
	}
	return answer, nil
}

func terminal(file *os.File) bool {
	var settings syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&settings)))
	return errno == 0
}

func runSetup(explicitPath string, setupOnly, interactive bool, in io.Reader, out io.Writer) (config.Config, error) {
	path := explicitPath
	var exists bool
	if path == "" {
		var err error
		path, exists, err = config.Find()
		if err != nil {
			return config.Config{}, err
		}
	} else if _, err := os.Lstat(path); err == nil {
		exists = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return config.Config{}, err
	}
	p := setupPrompts{lines: bufio.NewScanner(in), out: out}
	if exists {
		return p.loadExisting(path, setupOnly, interactive)
	}
	if !setupOnly && (!interactive || explicitPath != "") {
		return config.Config{}, fmt.Errorf("configuration not found at %s; run netwatch setup (or netwatch setup -config PATH)", path)
	}

	fmt.Fprintln(out, "NETWATCH first-run setup")
	defaultPath := path
	if explicitPath == "" {
		var err error
		path, err = p.ask("Config path (automatic)", path)
		if err != nil {
			return config.Config{}, err
		}
	}
	path, err := absolutePath(path)
	if err != nil {
		return config.Config{}, err
	}
	// A manually entered path may point to an existing configuration.
	if _, err := os.Lstat(path); err == nil {
		cfg, err := p.loadExisting(path, setupOnly, interactive)
		if err == nil {
			p.rememberConfig(defaultPath, path, explicitPath != "")
		}
		return cfg, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return config.Config{}, err
	}

	cfg := config.Default()
	if err := p.configureLogs(&cfg); err != nil {
		return config.Config{}, err
	}
	if err := p.configureGeoIP(path, &cfg); err != nil {
		return config.Config{}, err
	}
	if err := config.CreateSetup(path, cfg); err != nil {
		return config.Config{}, err
	}
	fmt.Fprintln(out, "Saved configuration:", path)
	p.rememberConfig(defaultPath, path, explicitPath != "")
	return cfg, nil
}

func (p setupPrompts) rememberConfig(defaultPath, path string, explicit bool) {
	if explicit || path == defaultPath {
		return
	}
	if err := os.MkdirAll(filepath.Dir(defaultPath), 0700); err == nil {
		if err := os.Symlink(path, defaultPath); err == nil {
			fmt.Fprintln(p.out, "Saved configuration link:", defaultPath)
			return
		}
	}
	fmt.Fprintln(p.out, "To use this configuration again, pass -config with the saved path.")
}

func (p setupPrompts) loadExisting(path string, setupOnly, interactive bool) (config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return config.Config{}, err
	}
	if (interactive || setupOnly) && cfg.GeoIPPrompt && cfg.GeoIPIPv4DB == "" && cfg.GeoIPIPv6DB == "" {
		if err := p.configureGeoIP(path, &cfg); err != nil {
			return config.Config{}, err
		}
		if !cfg.GeoIPPrompt {
			if err := config.SaveGeoSetup(path, cfg); err != nil {
				return config.Config{}, err
			}
		}
	} else if setupOnly {
		fmt.Fprintln(p.out, "Configuration already exists:", path)
	}
	return cfg, nil
}

func absolutePath(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	}
	return filepath.Abs(path)
}

func openRegular(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	return os.Open(path)
}

func (p setupPrompts) configureLogs(cfg *config.Config) error {
	logs := [][2]string{
		{cfg.AccessLog, cfg.ErrorLog},
		{"/var/log/httpd/access_log", "/var/log/httpd/error_log"},
		{"/var/log/apache2/access.log", "/var/log/apache2/error.log"},
	}
	for _, pair := range logs {
		f, err := openRegular(pair[0])
		if err == nil {
			f.Close()
			cfg.AccessLog, cfg.ErrorLog = pair[0], pair[1]
			break
		}
	}
	for {
		path, err := p.ask("Access log path (automatic)", cfg.AccessLog)
		if err != nil {
			return err
		}
		path, err = absolutePath(path)
		if err != nil {
			return err
		}
		f, err := openRegular(path)
		if err != nil {
			fmt.Fprintln(p.out, err)
			fmt.Fprintln(p.out, "Enter a readable access log path or press Ctrl+C to exit setup.")
			continue
		}
		cfg.AccessLog = path
		cfg.AccessFormat = detectAccessFormat(f)
		f.Close()
		break
	}
	if name, ok := map[string]string{"access.log": "error.log", "access_log": "error_log"}[filepath.Base(cfg.AccessLog)]; ok {
		cfg.ErrorLog = filepath.Join(filepath.Dir(cfg.AccessLog), name)
	}
	path, err := p.ask("Error log path (automatic)", cfg.ErrorLog)
	if err != nil {
		return err
	}
	cfg.ErrorLog, err = absolutePath(path)
	return err
}

func detectAccessFormat(r io.Reader) string {
	scanner := bufio.NewScanner(io.LimitReader(r, 64*1024))
	for i := 0; i < 12 && scanner.Scan(); i++ {
		for _, format := range []string{"combined", "common", "json"} {
			if _, err := event.ParseAccess(scanner.Text(), format); err == nil {
				return format
			}
		}
	}
	return "combined"
}

func (p setupPrompts) configureGeoIP(configPath string, cfg *config.Config) error {
	dataDir, err := config.GeoDataDir()
	if err != nil {
		dataDir = filepath.Join(filepath.Dir(configPath), "geoip")
	}
	if target, err := filepath.EvalSymlinks(configPath); err == nil {
		configPath = target
	}
	v4, v6 := findGeoIP(filepath.Dir(configPath), dataDir, "data")
	label := "Download DB-IP Lite? (y/n/never/path; never = do not ask again)"
	fallback := "N"
	if v4 != "" || v6 != "" {
		fmt.Fprintf(p.out, "Found GeoIP databases (IPv4: %s, IPv6: %s)\n", v4, v6)
		label = "GeoIP (auto = use detected files, y = download, n = skip, never = do not ask again, path)"
		fallback = "auto"
	}
	fmt.Fprintln(p.out, geoip.DBIPAttribution)
	for {
		choice, err := p.ask(label, fallback)
		if err != nil {
			return err
		}
		switch strings.ToLower(choice) {
		case "auto":
			if v4 == "" && v6 == "" {
				fmt.Fprintln(p.out, "No usable GeoIP databases found in the standard locations.")
				continue
			}
			cfg.GeoIPIPv4DB, cfg.GeoIPIPv6DB, cfg.GeoIPPrompt = v4, v6, false
			if v4 != "" && v6 != "" {
				if _, err := os.Stat(filepath.Join(filepath.Dir(v4), "ATTRIBUTION.txt")); err == nil {
					cfg.GeoIPSource = "db-ip"
				}
			}
			return nil
		case "path":
			return p.enterGeoPaths(cfg, v4, v6)
		case "n", "no", "skip":
			cfg.GeoIPPrompt = true
			return nil
		case "never":
			cfg.GeoIPPrompt = false
			return nil
		case "y", "yes", "download":
			return p.downloadGeoIP(cfg, dataDir)
		default:
			fmt.Fprintln(p.out, "Choose y, n, never (do not ask again), or path.")
		}
	}
}

func findGeoIP(dirs ...string) (v4, v6 string) {
	for _, dir := range dirs {
		if a, b := findGeoIPPair(dir); a != "" || b != "" {
			return a, b
		}
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), "dbip-") {
				subdir := filepath.Join(dir, entry.Name())
				if _, err := os.Stat(filepath.Join(subdir, "ATTRIBUTION.txt")); err != nil {
					continue
				}
				if a, b := findGeoIPPair(subdir); a != "" && b != "" {
					return a, b
				}
			}
		}
	}
	return "", ""
}

func findGeoIPPair(dir string) (v4, v6 string) {
	for _, name := range []string{"ipv4.nwgeo", "ipv4.csv"} {
		if path, err := checkedGeoPath(filepath.Join(dir, name), 4); err == nil {
			v4 = path
			break
		}
	}
	for _, name := range []string{"ipv6.nwgeo", "ipv6.csv"} {
		if path, err := checkedGeoPath(filepath.Join(dir, name), 6); err == nil {
			v6 = path
			break
		}
	}
	return v4, v6
}

func checkedGeoPath(text string, family int) (string, error) {
	if text == "-" || text == "" {
		return "", nil
	}
	path, err := absolutePath(text)
	if err != nil {
		return "", err
	}
	f, err := openRegular(path)
	if err != nil {
		return "", err
	}
	f.Close()
	var v4, v6 string
	if family == 4 {
		v4 = path
	} else {
		v6 = path
	}
	r, err := geoip.Open(v4, v6)
	if err != nil {
		return "", err
	}
	return path, r.Close()
}

func (p setupPrompts) enterGeoPaths(cfg *config.Config, v4, v6 string) error {
	for _, field := range []struct {
		family int
		path   *string
		auto   string
	}{{4, &cfg.GeoIPIPv4DB, v4}, {6, &cfg.GeoIPIPv6DB, v6}} {
		fallback := field.auto
		if fallback == "" {
			fallback = "-"
		}
		for {
			text, err := p.ask(fmt.Sprintf("IPv%d GeoIP path (- to skip)", field.family), fallback)
			if err != nil {
				return err
			}
			path, err := checkedGeoPath(text, field.family)
			if err != nil {
				fmt.Fprintln(p.out, err)
				continue
			}
			*field.path = path
			break
		}
	}
	cfg.GeoIPPrompt = cfg.GeoIPIPv4DB == "" && cfg.GeoIPIPv6DB == ""
	return nil
}

func (p setupPrompts) downloadGeoIP(cfg *config.Config, dataDir string) error {
	for {
		fmt.Fprintln(p.out, "1. Countries (small download)\n2. Regions (city download, smaller installed files)\n3. Cities (largest installed files)")
		answer, err := p.ask("GeoIP detail", "1")
		if err != nil {
			return err
		}
		detail := map[string]string{"1": "country", "2": "region", "3": "city"}[answer]
		if detail == "" {
			fmt.Fprintln(p.out, "Choose 1, 2 or 3.")
			continue
		}
		fmt.Fprintln(p.out, "Downloading and converting IPv4 and IPv6 databases…")
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		dir, err := geoip.DownloadDBIP(ctx, dataDir, detail)
		stop()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			fmt.Fprintf(p.out, "GeoIP download failed: %v\nContinuing without GeoIP. NETWATCH will ask again on the next interactive start.\n", err)
			cfg.GeoIPPrompt = true
			return nil
		}
		cfg.GeoIPIPv4DB = filepath.Join(dir, "ipv4.nwgeo")
		cfg.GeoIPIPv6DB = filepath.Join(dir, "ipv6.nwgeo")
		cfg.GeoIPSource, cfg.GeoIPPrompt = "db-ip", false
		fmt.Fprintln(p.out, "Installed GeoIP databases:", dir)
		return nil
	}
}
