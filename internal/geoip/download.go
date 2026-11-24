package geoip

import (
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

const DBIPAttribution = "IP Geolocation by DB-IP (https://db-ip.com), CC BY 4.0 (https://creativecommons.org/licenses/by/4.0/)"

//go:embed country_points.json
var countryPointsJSON []byte

// DownloadDBIP installs a pair of NWGEO files in a new directory under dataDir.
// The completed download can be discovered again if config saving fails.
func DownloadDBIP(ctx context.Context, dataDir, detail string, progress io.Writer) (dir string, usedSystemDNS bool, err error) {
	now := time.Now().UTC()
	var fallbackUsed atomic.Bool
	transport := dbipTransport(&fallbackUsed)
	defer transport.CloseIdleConnections()
	dir, err = downloadDBIP(ctx, dataDir, detail, "https://download.db-ip.com/free", now, transport, progress)
	return dir, fallbackUsed.Load(), err
}

func downloadDBIP(ctx context.Context, dataDir, detail, baseURL string, now time.Time, transport http.RoundTripper, progress io.Writer) (string, error) {
	var edition string
	switch detail {
	case "country":
		edition = "country"
	case "region", "city":
		edition = "city"
	default:
		return "", fmt.Errorf("unsupported GeoIP detail %q", detail)
	}

	client := &http.Client{Timeout: 10 * time.Minute, Transport: transport}
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	for attempt := 0; attempt < 2; attempt++ {
		url := fmt.Sprintf("%s/dbip-%s-lite-%s.csv.gz", baseURL, edition, month.Format("2006-01"))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept-Encoding", "identity")
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		status := resp.Status
		switch resp.StatusCode {
		case http.StatusOK:
			defer resp.Body.Close()
			body := &archiveProgress{source: resp.Body, out: progress, total: resp.ContentLength}
			if progress != nil {
				body.report()
			}
			return installDBIP(ctx, body, dataDir, detail, url, progress)
		case http.StatusNotFound:
			resp.Body.Close()
			month = month.AddDate(0, -1, 0)
			if attempt == 0 {
				continue
			}
		default:
			resp.Body.Close()
		}
		return "", fmt.Errorf("download %s: HTTP %s", url, status)
	}
	return "", fmt.Errorf("no monthly DB-IP archive available")
}

func installDBIP(ctx context.Context, src io.Reader, dataDir, detail, source string, progress io.Writer) (string, error) {
	gz, err := gzip.NewReader(src)
	if err != nil {
		return "", err
	}
	defer gz.Close()

	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(dataDir, "dbip-"+detail+"-")
	if err != nil {
		return "", err
	}
	success := false
	defer func() {
		if !success {
			os.RemoveAll(dir)
		}
	}()
	v4, err := os.Create(filepath.Join(dir, "ipv4.csv"))
	if err != nil {
		return "", err
	}
	defer v4.Close()
	v6, err := os.Create(filepath.Join(dir, "ipv6.csv"))
	if err != nil {
		return "", err
	}
	defer v6.Close()

	// Bound the uncompressed input so an unexpectedly large archive cannot grow indefinitely.
	const maxCSVBytes = 2 << 30
	limited := &io.LimitedReader{R: gz, N: maxCSVBytes}
	if err := normalizeDBIP(ctx, limited, v4, v6, detail); err != nil {
		return "", err
	}
	if limited.N == 0 {
		return "", fmt.Errorf("DB-IP CSV exceeds %d bytes", maxCSVBytes)
	}
	for _, input := range []struct {
		family int
		file   *os.File
	}{{4, v4}, {6, v6}} {
		family, file := input.family, input.file
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := file.Close(); err != nil {
			return "", err
		}
		dest := filepath.Join(dir, fmt.Sprintf("ipv%d.nwgeo", family))
		if progress != nil {
			fmt.Fprintf(progress, "Converting IPv%d to NWGEO…\n", family)
		}
		if err := convertCSV(ctx, file.Name(), dest, family); err != nil {
			return "", fmt.Errorf("convert IPv%d: %w", family, err)
		}
		if err := os.Remove(file.Name()); err != nil {
			return "", err
		}
	}
	credit := fmt.Sprintf("%s\nSource: %s\nAdapted to NETWATCH NWGEO (%s detail).\nCountry map points: Natural Earth, public domain (https://www.naturalearthdata.com/).\n", DBIPAttribution, source, detail)
	if err := os.WriteFile(filepath.Join(dir, "ATTRIBUTION.txt"), []byte(credit), 0600); err != nil {
		return "", err
	}
	success = true
	return dir, nil
}

type dbipRange struct {
	start, end netip.Addr
	location   [6]string
}

type dbipWriter struct {
	csv  *csv.Writer
	last dbipRange
}

func (w *dbipWriter) add(r dbipRange) error {
	if w.last.end.Next() == r.start && w.last.location == r.location {
		w.last.end = r.end
		return nil
	}
	if err := w.flushRange(); err != nil {
		return err
	}
	w.last = r
	return nil
}

func (w *dbipWriter) flushRange() error {
	if !w.last.start.IsValid() {
		return nil
	}
	ipNumber := func(ip netip.Addr) string {
		return new(big.Int).SetBytes(ip.AsSlice()).String()
	}
	return w.csv.Write(append([]string{ipNumber(w.last.start), ipNumber(w.last.end)}, w.last.location[:]...))
}

func normalizeDBIP(ctx context.Context, src io.Reader, ipv4, ipv6 io.Writer, detail string) error {
	var countryPoints map[string][2]float64
	if detail == "country" {
		if err := json.Unmarshal(countryPointsJSON, &countryPoints); err != nil {
			return err
		}
	}
	regionPoints := make(map[[2]string][2]string)
	reader := csv.NewReader(src)
	reader.FieldsPerRecord = 8
	if detail == "country" {
		reader.FieldsPerRecord = 3
	}
	reader.ReuseRecord = true
	v4 := dbipWriter{csv: csv.NewWriter(ipv4)}
	v6 := dbipWriter{csv: csv.NewWriter(ipv6)}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		start, startErr := netip.ParseAddr(row[0])
		end, endErr := netip.ParseAddr(row[1])
		if startErr != nil || endErr != nil || start.BitLen() != end.BitLen() || start.Zone() != "" || end.Zone() != "" || start.Compare(end) > 0 {
			return fmt.Errorf("invalid DB-IP range %q–%q", row[0], row[1])
		}
		var location [6]string
		if detail == "country" {
			point, ok := countryPoints[row[2]]
			if !ok {
				continue
			}
			location = [6]string{row[2], "", "", "", strconv.FormatFloat(point[0], 'f', -1, 64), strconv.FormatFloat(point[1], 'f', -1, 64)}
		} else {
			location = [6]string{row[3], "", row[4], row[5], row[6], row[7]}
			if detail == "region" {
				// A region uses one approximate point from the source, across both IP families.
				key := [2]string{row[3], row[4]}
				point, ok := regionPoints[key]
				if !ok {
					point = [2]string{row[6], row[7]}
					regionPoints[key] = point
				}
				location[3], location[4], location[5] = "", point[0], point[1]
			}
		}
		writer := &v6
		if start.Is4() {
			writer = &v4
		}
		if err := writer.add(dbipRange{start: start, end: end, location: location}); err != nil {
			return err
		}
	}
	for _, writer := range []*dbipWriter{&v4, &v6} {
		if err := writer.flushRange(); err != nil {
			return err
		}
		writer.csv.Flush()
		if err := writer.csv.Error(); err != nil {
			return err
		}
	}
	return nil
}
