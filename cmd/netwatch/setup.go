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
		fmt.Fprintln(out, "Configuration already exists:", path)
		return nil
	}

	cfg := config.Default()
	cfg.AccessLog = suggestedAccessLog(cfg.AccessLog)
	log, err := openRegular(cfg.AccessLog)
	if err != nil {
		return fmt.Errorf("no readable access log found at the standard paths; run netwatch setup to choose one (%w)", err)
	}
	format, detectErr := detectAccessFormat(log)
	log.Close()
	if detectErr != nil {
		fmt.Fprintf(out, "%v; using %s.\n", detectErr, cfg.AccessFormat)
	} else {
		cfg.AccessFormat = format
	}
	configDir, dataDir := setupGeoPaths(path)
	cfg.GeoIPIPv4DB, cfg.GeoIPIPv6DB, cfg.GeoIPSource = findGeoIP(configDir, dataDir, "data")
	if err := config.CreateSetup(path, cfg); err != nil {
		return err
	}
	fmt.Fprintln(out, "Saved configuration:", path)
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
	fmt.Fprintln(out, "Saved configuration:", path)
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

func suggestedAccessLog(defaultPath string) string {
	for _, path := range []string{defaultPath, "/var/log/httpd/access_log", "/var/log/apache2/access.log"} {
		f, err := openRegular(path)
		if err == nil {
			f.Close()
			return path
		}
	}
	return defaultPath
}

func (p setupPrompts) configureAccessLog(cfg *config.Config) error {
	cfg.AccessLog = suggestedAccessLog(cfg.AccessLog)
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
		format, detectErr := detectAccessFormat(f)
		f.Close()
		if detectErr == nil {
			cfg.AccessFormat = format
			return nil
		}

		fmt.Fprintln(p.out, detectErr)
		for {
			answer, err := p.ask("Access log format (combined/common/main/json)", config.Default().AccessFormat)
			if err != nil {
				return err
			}
			cfg.AccessFormat = strings.ToLower(answer)
			if err := cfg.Validate(); err != nil {
				fmt.Fprintln(p.out, err)
				continue
			}
			return nil
		}
	}
}

func detectAccessFormat(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(io.LimitReader(r, 64*1024))
	detected := ""
	for i := 0; i < 12 && scanner.Scan(); i++ {
		for _, format := range []string{"combined", "common", "json"} {
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
		fmt.Fprintf(p.out, "Found GeoIP databases (IPv4: %s, IPv6: %s)\n", v4, v6)
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
