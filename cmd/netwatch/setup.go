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
	"slices"
	"strings"
	"syscall"
	"unsafe"

	"netwatch/internal/config"
	"netwatch/internal/event"
	"netwatch/internal/geoip"
	"netwatch/internal/regularfile"
)

type setupPrompts struct {
	lines *bufio.Scanner
	out   io.Writer
}

func (p setupPrompts) ask(label, fallback string) (string, error) {
	fmt.Fprintf(p.out, "%s [%s]: ", label, cleanDiagnostic(fallback))
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

func setupConfigPath(explicitPath string) (string, bool, error) {
	if explicitPath == "" {
		return config.Find()
	}
	_, err := os.Lstat(explicitPath)
	if errors.Is(err, os.ErrNotExist) {
		return explicitPath, false, nil
	}
	return explicitPath, err == nil, err
}

func runAutoConfig(explicitPath string, out io.Writer) error {
	path, exists, err := setupConfigPath(explicitPath)
	if err != nil {
		return err
	}
	if exists {
		if _, err := config.Load(path); err != nil {
			return err
		}
		fmt.Fprintln(out, "Configuration already exists:", cleanDiagnostic(path))
		return nil
	}

	cfg := config.Default()
	candidates := discoverAccessLogs("/var/log")
	if len(candidates) > 0 {
		cfg.AccessLog = candidates[0].path
		cfg.AccessFormat = candidates[0].fallbackFormat()
	}
	log, err := openRegular(cfg.AccessLog)
	if err != nil {
		return fmt.Errorf("no readable access log found at the standard paths; run netwatch setup to choose one (%w)", err)
	}
	info, statErr := log.Stat()
	format, detectErr := detectAccessFormat(log)
	log.Close()
	if statErr != nil {
		return statErr
	}
	if detectErr != nil {
		if info.Size() > 0 {
			return fmt.Errorf("%w; run netwatch setup to choose the format", detectErr)
		}
		fmt.Fprintf(out, "%v; using %s.\n", detectErr, cfg.AccessFormat)
	} else {
		cfg.AccessFormat = format
	}
	configDir, dataDir := setupGeoPaths(path)
	cfg.GeoIPIPv4DB, cfg.GeoIPIPv6DB, cfg.GeoIPSource = findGeoIP(configDir, dataDir, "data")
	if err := config.CreateSetup(path, cfg); err != nil {
		return err
	}
	fmt.Fprintln(out, "Saved configuration:", cleanDiagnostic(path))
	return nil
}

func runSetup(explicitPath string, setupOnly, interactive bool, in io.Reader, out io.Writer) (config.Config, error) {
	path, exists, err := setupConfigPath(explicitPath)
	if err != nil {
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
	cfg := config.Default()
	if err := p.configureAccessLog(&cfg); err != nil {
		return config.Config{}, err
	}
	if err := p.configureGeoIP(path, &cfg); err != nil {
		return config.Config{}, err
	}
	if err := config.CreateSetup(path, cfg); err != nil {
		return config.Config{}, err
	}
	fmt.Fprintln(out, "Saved configuration:", cleanDiagnostic(path))
	return cfg, nil
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
			return config.Load(path)
		}
	} else if setupOnly {
		fmt.Fprintln(p.out, "Configuration already exists:", cleanDiagnostic(path))
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
	file, _, err := regularfile.Open(path)
	return file, err
}

type accessLogCandidate struct {
	server string
	path   string
}

func (c accessLogCandidate) fallbackFormat() string {
	switch c.server {
	case "caddy":
		return "json"
	case "envoy", "traefik":
		return c.server
	case "apache":
		if filepath.Base(c.path) == "other_vhosts_access.log" {
			return "apache-vhost"
		}
	}
	return "combined"
}

func discoverAccessLogs(root string) []accessLogCandidate {
	var candidates []accessLogCandidate
	for _, source := range []struct {
		name     string
		patterns []string
	}{
		{"nginx", []string{"nginx/access.log", "nginx/access*.log", "nginx/*access*.log"}},
		{"apache", []string{"httpd/access_log", "apache2/access.log", "httpd/*access*log", "apache2/*access*.log"}},
		{"caddy", []string{"caddy/access.log", "caddy/*access*.log"}},
		{"envoy", []string{"envoy/access.log", "envoy/*access*.log"}},
		{"traefik", []string{"traefik/access.log", "traefik/*access*.log"}},
	} {
		var paths []string
		for _, pattern := range source.patterns {
			matches, _ := filepath.Glob(filepath.Join(root, pattern))
			for _, path := range matches {
				if slices.Contains(paths, path) {
					continue
				}
				if f, err := openRegular(path); err == nil {
					f.Close()
					paths = append(paths, path)
					candidates = append(candidates, accessLogCandidate{source.name, path})
				}
			}
		}
	}
	return candidates
}

func (p setupPrompts) chooseAccessLog(candidates []accessLogCandidate, fallback string) (string, error) {
	var servers []string
	for _, c := range candidates {
		if !slices.Contains(servers, c.server) {
			servers = append(servers, c.server)
		}
	}
	if len(candidates) == 0 {
		return p.ask("Access log path (automatic)", fallback)
	}
	if len(candidates) == 1 {
		return p.ask("Access log path (automatic)", candidates[0].path)
	}
	answer, err := p.ask("Access log source ("+strings.Join(servers, "/")+"/path)", servers[0])
	if err != nil {
		return "", err
	}
	if strings.EqualFold(answer, "path") {
		return p.ask("Access log path", candidates[0].path)
	}
	var paths []string
	for _, c := range candidates {
		if strings.EqualFold(answer, c.server) {
			paths = append(paths, c.path)
		}
	}
	if len(paths) == 1 {
		return paths[0], nil
	}
	if len(paths) > 1 {
		for _, path := range paths {
			fmt.Fprintln(p.out, cleanDiagnostic(path))
		}
		return p.ask("Access log path", paths[0])
	}
	return answer, nil
}

func (p setupPrompts) configureAccessLog(cfg *config.Config) error {
	for {
		candidates := discoverAccessLogs("/var/log")
		path, err := p.chooseAccessLog(candidates, cfg.AccessLog)
		if err != nil {
			return err
		}
		path, err = absolutePath(path)
		if err != nil {
			return err
		}
		f, err := openRegular(path)
		if err != nil {
			fmt.Fprintln(p.out, cleanDiagnostic(err.Error()))
			fmt.Fprintln(p.out, "Enter a readable access log path or press Ctrl+C to exit setup.")
			continue
		}
		cfg.AccessLog = path
		format, detectErr := detectAccessFormat(f)
		f.Close()
		if detectErr == nil {
			cfg.AccessFormat = format
			return nil
		}

		fmt.Fprintln(p.out, detectErr)
		fallback := config.Default().AccessFormat
		for _, c := range candidates {
			if c.path == path {
				fallback = c.fallbackFormat()
				break
			}
		}
		for {
			answer, err := p.ask("Access log format ("+strings.Join(event.SampleFormats(), "/")+"/nginx/main)", fallback)
			if err != nil {
				return err
			}
			cfg.AccessFormat = strings.ToLower(answer)
			cfg.NginxLogFormat = ""
			if cfg.AccessFormat == "nginx" {
				cfg.NginxLogFormat, err = p.ask("Nginx log_format directive or single-line template", "")
				if err != nil {
					return err
				}
			}
			if err := cfg.Validate(); err != nil {
				fmt.Fprintln(p.out, cleanDiagnostic(err.Error()))
				continue
			}
			return nil
		}
	}
}

func detectAccessFormat(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(io.LimitReader(r, 64*1024))
	detected := ""
	formats := event.SampleFormats()
	for i := 0; i < 12 && scanner.Scan(); i++ {
		for _, format := range formats {
			if _, err := event.ParseAccess(scanner.Text(), format); err == nil {
				if detected != "" && detected != format {
					return "", fmt.Errorf("access log sample matches multiple formats (%s and %s)", detected, format)
				}
				detected = format
				break
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("could not sample access log: %w", err)
	}
	if detected == "" {
		return "", errors.New("could not detect the access log format")
	}
	return detected, nil
}

func setupGeoPaths(configPath string) (configDir, dataDir string) {
	dataDir, err := config.GeoDataDir()
	if err != nil {
		dataDir = filepath.Join(filepath.Dir(configPath), "geoip")
	}
	if target, err := filepath.EvalSymlinks(configPath); err == nil {
		configPath = target
	}
	return filepath.Dir(configPath), dataDir
}

func (p setupPrompts) configureGeoIP(configPath string, cfg *config.Config) error {
	configDir, dataDir := setupGeoPaths(configPath)
	v4, v6, source := findGeoIP(configDir, dataDir, "data")
	label := "Download DB-IP Lite for GeoIP? (y/n/never/path)"
	fallback := "Y"
	if v4 != "" || v6 != "" {
		fmt.Fprintf(p.out, "Found GeoIP databases (IPv4: %s, IPv6: %s)\n", cleanDiagnostic(v4), cleanDiagnostic(v6))
		label = "GeoIP (auto = use detected files, y = download DB-IP Lite, n = skip, never, path)"
		fallback = "auto"
	}
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
			cfg.GeoIPIPv4DB, cfg.GeoIPIPv6DB = v4, v6
			cfg.GeoIPSource, cfg.GeoIPPrompt = source, false
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
			fmt.Fprintln(p.out, "Choose y, n, never, or path.")
		}
	}
}

func findGeoIP(dirs ...string) (v4, v6, source string) {
	for _, dir := range dirs {
		if a, b := findGeoIPPair(dir); a != "" || b != "" {
			return a, b, ""
		}
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "dbip-") {
				continue
			}
			subdir := filepath.Join(dir, entry.Name())
			if _, err := os.Stat(filepath.Join(subdir, "ATTRIBUTION.txt")); err != nil {
				continue
			}
			if a, b := findGeoIPPair(subdir); a != "" && b != "" {
				return a, b, "db-ip"
			}
		}
	}
	return "", "", ""
}

func findGeoIPPair(dir string) (v4, v6 string) {
	find := func(family int) string {
		for _, extension := range []string{".nwgeo", ".csv"} {
			name := fmt.Sprintf("ipv%d%s", family, extension)
			if path, err := checkedGeoPath(filepath.Join(dir, name), family); err == nil {
				return path
			}
		}
		return ""
	}
	return find(4), find(6)
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
				fmt.Fprintln(p.out, cleanDiagnostic(err.Error()))
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
	fmt.Fprintln(p.out, geoip.DBIPAttribution)
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
		fmt.Fprintln(p.out, "Connecting to DB-IP…")
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		dir, usedSystemDNS, err := geoip.DownloadDBIP(ctx, dataDir, detail, p.out)
		stop()
		if usedSystemDNS {
			fmt.Fprintln(p.out, "GeoIP download used the system DNS fallback.")
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			fmt.Fprintf(p.out, "GeoIP download failed: %s\nContinuing without GeoIP. NETWATCH will ask again on the next interactive start.\n", cleanDiagnostic(err.Error()))
			cfg.GeoIPPrompt = true
			return nil
		}
		cfg.GeoIPIPv4DB = filepath.Join(dir, "ipv4.nwgeo")
		cfg.GeoIPIPv6DB = filepath.Join(dir, "ipv6.nwgeo")
		cfg.GeoIPSource, cfg.GeoIPPrompt = "db-ip", false
		fmt.Fprintln(p.out, "Installed GeoIP databases:", cleanDiagnostic(dir))
		return nil
	}
}
