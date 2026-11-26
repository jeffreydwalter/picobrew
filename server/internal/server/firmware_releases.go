package server

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/firmware"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

// A fresh index drops cached development listings when upgrading an existing Hub.
const firmwareCatalogKey = "firmware-release-index-v2"

type releaseNotice struct {
	DeviceID string `json:"device_id"`
	Name     string `json:"name"`
	Version  string `json:"version"`
}
type releaseView struct {
	Checked  *time.Time         `json:"checked,omitempty"`
	Releases []firmware.Release `json:"releases"`
	Notices  []releaseNotice    `json:"notices"`
}

// The repository also publishes USB and reference images. Filter at both
// catalog boundaries so a Hub upgrade takes effect before the next refresh.
func otaCatalog(c firmware.ReleaseCatalog) firmware.ReleaseCatalog {
	c.Releases = slices.DeleteFunc(c.Releases, func(r firmware.Release) bool {
		return !r.WirelessPackage()
	})
	return c
}

func (s *Server) savedCatalog() (firmware.ReleaseCatalog, error) {
	var catalog firmware.ReleaseCatalog
	s.configMu.Lock()
	defer s.configMu.Unlock()
	cfg, err := s.readFirmwareSettings()
	if err != nil {
		return catalog, err
	}
	_, err = s.db.Get("config", firmwareCatalogKey, &catalog)
	if errors.Is(err, store.ErrNotFound) || (err == nil && catalog.Settings != cfg) {
		return firmware.ReleaseCatalog{Settings: cfg}, nil
	}
	return otaCatalog(catalog), err
}
func (s *Server) refreshCatalog(ctx context.Context) (firmware.ReleaseCatalog, error) {
	s.releaseMu.Lock()
	defer s.releaseMu.Unlock()
	catalog, err := s.savedCatalog()
	if err != nil {
		return catalog, err
	}
	next, err := s.firmwareGitHub.Releases(ctx, catalog.Settings)
	if err != nil {
		return catalog, err
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	current, err := s.readFirmwareSettings()
	if err != nil {
		return catalog, err
	}
	if current != next.Settings {
		return catalog, sourceChanged()
	}
	next = otaCatalog(next)
	_, err = s.db.Put("config", firmwareCatalogKey, next, -1)
	return next, err
}
func (s *Server) fleetForReleases() []domain.DeviceView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []domain.DeviceView
	for _, d := range s.devices {
		d.mu.RLock()
		if d.view.Adapter == "pico-standalone" && !d.identityMismatch && d.view.ModelError == "" {
			out = append(out, d.view)
		}
		d.mu.RUnlock()
	}
	return out
}

// Only compare numbered production releases for update notifications.
func newerRelease(a, b string) bool {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	if len(left) != 3 || len(right) != 3 {
		return false
	}
	cmp := 0
	for i := range left {
		x, e := strconv.ParseUint(left[i], 10, 32)
		y, f := strconv.ParseUint(right[i], 10, 32)
		if e != nil || f != nil {
			return false
		}
		if cmp == 0 {
			if x > y {
				cmp = 1
			} else if x < y {
				cmp = -1
			}
		}
	}
	return cmp > 0
}

// The public series starts at 1.0.0. Development firmware used larger
// version numbers and did not report a release channel.
func updateAvailable(catalog firmware.ReleaseCatalog, target firmware.Release, installed *domain.Capabilities) bool {
	if installed == nil || installed.FirmwareVersion == "" || installed.FirmwareVersion == target.FirmwareVersion {
		return false
	}
	version := installed.FirmwareVersion
	if catalog.ReleaseChannel == "stable" && installed.ReleaseChannel == "" &&
		((strings.HasPrefix(version, "1.") && newerRelease("1.3.11", version)) ||
			version == "preview-090fe78" || version == "preview-15739da") {
		return true
	}
	return newerRelease(target.FirmwareVersion, installed.FirmwareVersion)
}

func (s *Server) monitorReleases(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	var retryAfter time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if now.Before(retryAfter) || len(s.fleetForReleases()) == 0 {
				continue
			}
			catalog, err := s.savedCatalog()
			if err == nil && now.Sub(catalog.Checked) >= 24*time.Hour {
				_, err = s.refreshCatalog(ctx)
			}
			if err != nil && ctx.Err() == nil {
				s.cfg.Logger.Warn("Firmware version check failed", "error", err)
				retryAfter = now.Add(time.Hour)
			}
		}
	}
}
func (s *Server) firmwareUpdates(w http.ResponseWriter, r *http.Request) {
	c, err := s.savedCatalog()
	if err == nil && r.Method == http.MethodPost && !s.cfg.Demo {
		c, err = s.refreshCatalog(r.Context())
	}
	if err != nil {
		s.firmwareError(w, err)
		return
	}
	out := releaseView{Releases: []firmware.Release{}, Notices: []releaseNotice{}}
	if !c.Checked.IsZero() {
		out.Checked = &c.Checked
	}
	seen := map[string]bool{}
	for _, d := range s.fleetForReleases() {
		latest := ""
		for _, v := range c.Releases {
			if v.Model != d.Model || (v.Model == "pico-c" && d.Capabilities != nil && v.DisplayVariant != d.Capabilities.DisplayVariant) {
				continue
			}
			if !seen[v.ArchiveSHA256] {
				out.Releases = append(out.Releases, v)
				seen[v.ArchiveSHA256] = true
			}
			if updateAvailable(c, v, d.Capabilities) && (latest == "" || newerRelease(v.FirmwareVersion, latest)) {
				latest = v.FirmwareVersion
			}
		}
		if latest != "" {
			out.Notices = append(out.Notices, releaseNotice{DeviceID: d.ID, Name: d.Name, Version: latest})
		}
	}
	s.json(w, 200, out)
}
func (s *Server) stageRelease(w http.ResponseWriter, r *http.Request) {
	if !s.beginFirmwareWork(w) {
		return
	}
	defer s.firmwareWorkMu.Unlock()
	c, err := s.savedCatalog()
	if err != nil {
		s.firmwareError(w, err)
		return
	}
	id := chi.URLParam(r, "packageID")
	var selected *firmware.Release
	for _, release := range c.Releases {
		if release.ArchiveSHA256 == id {
			selected = &release
			break
		}
	}
	if selected == nil {
		s.fail(w, 404, "unknown_release", "Refresh available firmware versions and select the release again")
		return
	}
	var saved firmware.Package
	s.firmwareMu.Lock()
	_, err = s.db.Get(firmwareRecordKind, id, &saved)
	if err == nil {
		var archive []byte
		archive, err = s.readFirmwareArchive(id)
		if err == nil && saved.Candidate != selected.Candidate {
			err = &firmware.Error{Code: "firmware_package_invalid", Message: "The staged package metadata differs from the selected release"}
		}
		if err == nil {
			err = firmware.ValidateRelease(archive, *selected)
		}
	}
	s.firmwareMu.Unlock()
	if err == nil {
		s.json(w, 200, saved)
		return
	}
	if !errors.Is(err, store.ErrNotFound) && !isFirmwareCacheCorrupt(err) {
		s.firmwareError(w, err)
		return
	}
	data, err := s.firmwareGitHub.DownloadRelease(r.Context(), c, *selected)
	if err != nil {
		s.firmwareError(w, err)
		return
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	cfg, err := s.readFirmwareSettings()
	if err == nil && c.Settings != cfg {
		err = sourceChanged()
	}
	if err == nil {
		saved, _, err = s.saveFirmwarePackage(data, selected.Candidate, firmware.Provenance{Source: "library", Repository: c.Repository, Commit: c.Commit})
	}
	if err != nil {
		s.firmwareError(w, err)
		return
	}
	s.json(w, 200, saved)
}
