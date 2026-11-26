package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/firmware"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/privatefs"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/stockfirmware"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

const firmwareRecordKind = "firmware-package"

func (s *Server) firmwareError(w http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) {
		s.fail(w, 499, "cancelled", "The firmware request was canceled")
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		s.fail(w, 504, "firmware_timeout", "The firmware request timed out. Try again.")
		return
	}
	var failure *firmware.Error
	if errors.As(err, &failure) {
		status := http.StatusBadRequest
		switch failure.Code {
		case "firmware_package_invalid":
			status = http.StatusUnprocessableEntity
		case "firmware_github_not_found":
			status = http.StatusNotFound
		case "firmware_github_rate_limit", "firmware_busy":
			status = http.StatusTooManyRequests
		case "firmware_github_unavailable", "firmware_github_size":
			status = http.StatusBadGateway
		case "firmware_source_changed", "firmware_cache_full", "firmware_cache_corrupt":
			status = http.StatusConflict
		}
		s.fail(w, status, failure.Code, failure.Message)
		return
	}
	s.storeError(w, err)
}

// A single acquisition at a time bounds memory and temporary storage. Other
// authenticated requests fail promptly instead of queuing unlimited uploads.
func (s *Server) beginFirmwareWork(w http.ResponseWriter) bool {
	if !s.firmwareWorkMu.TryLock() {
		w.Header().Set("Retry-After", "1")
		s.fail(w, 429, "firmware_busy", "Another firmware upload or GitHub request is in progress. Try again shortly.")
		return false
	}
	return true
}

// The caller holds configMu. Loading settings never contacts GitHub.
func (s *Server) readFirmwareSettings() (firmware.Settings, error) {
	cfg := firmware.Settings{Repository: firmware.DefaultRepository}
	if _, err := s.db.Get("config", "firmware", &cfg); err != nil && !errors.Is(err, store.ErrNotFound) {
		return cfg, err
	}
	return firmware.NormalizeSettings(cfg)
}

func (s *Server) firmwareSettings(w http.ResponseWriter, r *http.Request) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if r.Method == http.MethodPut {
		var in firmware.Settings
		if !s.decode(w, r, &in) {
			return
		}
		cfg, err := firmware.NormalizeSettings(in)
		if err != nil {
			s.firmwareError(w, err)
			return
		}
		if _, err := s.db.Put("config", "firmware", cfg, -1); err != nil {
			s.internal(w, err)
			return
		}
		s.json(w, 200, cfg)
		return
	}
	cfg, err := s.readFirmwareSettings()
	if err != nil {
		s.firmwareError(w, err)
		return
	}
	s.json(w, 200, cfg)
}

func sourceChanged() error {
	return &firmware.Error{Code: "firmware_source_changed", Message: "The saved GitHub source changed. Check that source again before importing."}
}

func (s *Server) firmwareGitHubCatalog(w http.ResponseWriter, r *http.Request) {
	if !s.beginFirmwareWork(w) {
		return
	}
	defer s.firmwareWorkMu.Unlock()
	s.configMu.Lock()
	cfg, err := s.readFirmwareSettings()
	s.configMu.Unlock()
	if err != nil {
		s.firmwareError(w, err)
		return
	}
	catalog, err := s.firmwareGitHub.Catalog(r.Context(), cfg)
	if err != nil {
		s.firmwareError(w, err)
		return
	}
	s.configMu.Lock()
	current, err := s.readFirmwareSettings()
	s.configMu.Unlock()
	if err == nil && current != cfg {
		err = sourceChanged()
	}
	if err != nil {
		s.firmwareError(w, err)
		return
	}
	s.json(w, 200, catalog)
}

func (s *Server) firmwareGitHubImport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Repository     string `json:"repository"`
		Commit         string `json:"commit"`
		Model          string `json:"model"`
		DisplayVariant string `json:"display_variant"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	repository, err := firmware.NormalizeRepository(in.Repository)
	if err != nil {
		s.firmwareError(w, err)
		return
	}
	if !firmware.ValidCommit(in.Commit) || !firmware.ValidVariant(in.Model, in.DisplayVariant) {
		s.fail(w, 400, "firmware_github_selection", "Choose a supported model, an explicit C display variant and a full 40-character GitHub commit")
		return
	}
	if !s.beginFirmwareWork(w) {
		return
	}
	defer s.firmwareWorkMu.Unlock()
	s.configMu.Lock()
	cfg, err := s.readFirmwareSettings()
	s.configMu.Unlock()
	if err == nil && cfg.Repository != repository {
		err = sourceChanged()
	}
	if err != nil {
		s.firmwareError(w, err)
		return
	}
	archive, metadata, err := s.firmwareGitHub.Import(r.Context(), repository, in.Commit, in.Model, in.DisplayVariant)
	if err != nil {
		s.firmwareError(w, err)
		return
	}
	// Recheck immediately before publishing, under the same lock as settings
	// writes. A source edit during a download cannot publish a stale selection.
	s.configMu.Lock()
	current, err := s.readFirmwareSettings()
	if err == nil && current != cfg {
		err = sourceChanged()
	}
	var saved firmware.Package
	var created bool
	if err == nil {
		if err = r.Context().Err(); err == nil {
			saved, created, err = s.saveFirmwarePackage(archive, metadata, firmware.Provenance{
				Source: "github", Repository: repository, Commit: in.Commit,
			})
		}
	}
	s.configMu.Unlock()
	if err != nil {
		s.firmwareError(w, err)
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	s.json(w, code, saved)
}

func (s *Server) firmwarePackages(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.uploadFirmwarePackage(w, r)
		return
	}
	s.firmwareMu.Lock()
	packages, err := s.listFirmwarePackages()
	s.firmwareMu.Unlock()
	if err != nil {
		s.firmwareError(w, err)
		return
	}
	s.json(w, 200, packages)
}

func (s *Server) uploadFirmwarePackage(w http.ResponseWriter, r *http.Request) {
	if !s.beginFirmwareWork(w) {
		return
	}
	defer s.firmwareWorkMu.Unlock()
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "multipart/form-data" {
		s.fail(w, 415, "content_type", "Upload a firmware ZIP as multipart/form-data using the file field")
		return
	}
	// The archive is limited separately; this small allowance covers multipart
	// framing, not additional fields or additional files.
	r.Body = http.MaxBytesReader(w, r.Body, firmware.MaxArchiveBytes+(64<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		s.fail(w, 400, "firmware_upload", "The firmware upload is not valid multipart data")
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() == "" {
		s.fail(w, 400, "firmware_upload", "Upload one ZIP file using the file field")
		return
	}
	archive, readErr := io.ReadAll(io.LimitReader(part, firmware.MaxArchiveBytes+1))
	closeErr := part.Close()
	var tooLarge *http.MaxBytesError
	if len(archive) > firmware.MaxArchiveBytes || errors.As(readErr, &tooLarge) || errors.As(closeErr, &tooLarge) {
		s.fail(w, 413, "firmware_upload_size", "The firmware ZIP must be no larger than 2 MiB")
		return
	}
	if readErr != nil || closeErr != nil {
		s.fail(w, 400, "firmware_upload", "The firmware upload was incomplete")
		return
	}
	if next, err := reader.NextPart(); err != io.EOF {
		if next != nil {
			_ = next.Close()
		}
		if errors.As(err, &tooLarge) {
			s.fail(w, 413, "firmware_upload_size", "The firmware upload exceeds its size limit")
		} else {
			s.fail(w, 400, "firmware_upload", "Upload exactly one file without additional fields")
		}
		return
	}
	metadata, err := firmware.ValidateArchive(archive)
	if err != nil {
		s.firmwareError(w, err)
		return
	}
	if err := r.Context().Err(); err != nil {
		s.firmwareError(w, err)
		return
	}
	saved, created, err := s.saveFirmwarePackage(archive, metadata, firmware.Provenance{Source: "upload"})
	if err != nil {
		s.firmwareError(w, err)
		return
	}
	code := http.StatusOK
	if created {
		code = http.StatusCreated
	}
	s.json(w, code, saved)
}

func cacheCorrupt() error {
	return &firmware.Error{Code: "firmware_cache_corrupt", Message: "The saved firmware file is missing or has changed. Remove this package and import it again."}
}

func isFirmwareCacheCorrupt(err error) bool {
	var failure *firmware.Error
	return errors.As(err, &failure) && failure.Code == "firmware_cache_corrupt"
}

func cacheFull() error {
	return &firmware.Error{Code: "firmware_cache_full", Message: "The firmware cache holds at most 20 packages. Remove a saved package before adding another."}
}

func (s *Server) firmwareCacheDir() string {
	return filepath.Join(s.cfg.DataDir, "firmware")
}

func (s *Server) ensureFirmwareCache() error {
	dir := s.firmwareCacheDir()
	if err := privatefs.EnsureDir(dir); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return cacheCorrupt()
	}
	return nil
}

// The cache lock covers metadata and bytes together within this Hub process.
func (s *Server) listFirmwarePackages() ([]firmware.Package, error) {
	rows, err := s.db.List(firmwareRecordKind)
	if err != nil {
		return nil, err
	}
	out := make([]firmware.Package, 0, len(rows))
	for _, row := range rows {
		var p firmware.Package
		if err := json.Unmarshal(row, &p); err != nil {
			return nil, err
		}
		if !firmware.ValidID(p.ID) || p.Validation != "passed" {
			return nil, cacheCorrupt()
		}
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b firmware.Package) int {
		if a.CreatedAt.After(b.CreatedAt) {
			return -1
		}
		if a.CreatedAt.Before(b.CreatedAt) {
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s *Server) readFirmwareArchive(id string) ([]byte, error) {
	if !firmware.ValidID(id) {
		return nil, store.ErrNotFound
	}
	if err := s.ensureFirmwareCache(); err != nil {
		return nil, err
	}
	data, err := privatefs.ReadFile(filepath.Join(s.firmwareCacheDir(), id+".zip"), firmware.MaxReleaseBytes)
	if err != nil || len(data) > firmware.MaxReleaseBytes || firmware.Digest(data) != id {
		return nil, cacheCorrupt()
	}
	return data, nil
}

// Caller holds the cache publication lock. Publish complete bytes atomically,
// then read them back through the same private-file checks used for downloads.
func (s *Server) publishFirmwareArchive(id string, archive []byte) error {
	staging := filepath.Join(s.firmwareCacheDir(), ".incoming-"+randomID())
	defer os.Remove(staging)
	file, err := privatefs.OpenFile(staging, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	_, err = file.Write(archive)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(staging, filepath.Join(s.firmwareCacheDir(), id+".zip"))
	}
	if err != nil {
		return err
	}
	_, err = s.readFirmwareArchive(id)
	return err
}

func (s *Server) saveFirmwarePackage(archive []byte, metadata firmware.Candidate, provenance firmware.Provenance) (firmware.Package, bool, error) {
	s.firmwareMu.Lock()
	defer s.firmwareMu.Unlock()
	var existing firmware.Package
	lock, err := s.lockFirmwareCache()
	if err != nil {
		return existing, false, err
	}
	defer lock.Close()
	if err := s.recoverFirmwareCache(); err != nil {
		return existing, false, err
	}
	id := firmware.Digest(archive)
	if _, err := s.db.Get(firmwareRecordKind, id, &existing); err == nil {
		if _, err := s.readFirmwareArchive(id); err != nil {
			if provenance.Source != "library" || existing.Candidate != metadata || !isFirmwareCacheCorrupt(err) {
				return existing, false, err
			}
			info, statErr := os.Lstat(filepath.Join(s.firmwareCacheDir(), id+".zip"))
			if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				return existing, false, statErr
			}
			if info != nil && !info.Mode().IsRegular() {
				return existing, false, cacheCorrupt()
			}
			// DownloadRelease verified the chosen catalog digest and all package
			// contents. Keep the original package record and provenance.
			return existing, false, s.publishFirmwareArchive(id, archive)
		}
		// Duplicate acquisition never upgrades the original source's claims.
		return existing, false, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return existing, false, err
	}
	packages, err := s.listFirmwarePackages()
	if err != nil {
		return existing, false, err
	}
	if provenance.Source != "library" && len(packages) >= firmware.MaxPackages {
		return existing, false, cacheFull()
	}
	if err := s.ensureFirmwareCache(); err != nil {
		return existing, false, err
	}
	dir := s.firmwareCacheDir()
	destination := filepath.Join(dir, id+".zip")
	files, err := os.ReadDir(dir)
	if err != nil {
		return existing, false, err
	}
	count := 0
	var total int64
	for _, entry := range files {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return existing, false, cacheCorrupt()
		}
		// Include interrupted staged/orphan files in the disk bound.
		total += info.Size()
		if strings.HasSuffix(entry.Name(), ".zip") && entry.Name() != id+".zip" {
			count++
		}
	}
	if provenance.Source != "library" && (count >= firmware.MaxPackages || total+int64(len(archive)) > firmware.MaxPackages*firmware.MaxReleaseBytes) {
		return existing, false, cacheFull()
	}
	createdFile := false
	if _, err := os.Lstat(destination); err == nil {
		if _, err := s.readFirmwareArchive(id); err != nil {
			return existing, false, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return existing, false, err
	} else {
		if err := s.publishFirmwareArchive(id, archive); err != nil {
			return existing, false, err
		}
		createdFile = true
	}

	p := firmware.Package{Candidate: metadata, ID: id, PackageBytes: len(archive),
		CreatedAt: time.Now().UTC(), Validation: "passed", Provenance: provenance}
	if _, err := s.db.Put(firmwareRecordKind, id, p, 0); err != nil {
		// Another Hub handle may have published the same content first.
		if errors.Is(err, store.ErrConflict) {
			if _, getErr := s.db.Get(firmwareRecordKind, id, &existing); getErr == nil {
				return existing, false, nil
			}
		}
		if createdFile {
			var check firmware.Package
			if _, getErr := s.db.Get(firmwareRecordKind, id, &check); errors.Is(getErr, store.ErrNotFound) {
				_ = os.Remove(destination)
			}
		}
		return existing, false, err
	}
	return p, true, nil
}

func (s *Server) firmwarePackage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "packageID")
	if !firmware.ValidID(id) {
		s.storeError(w, store.ErrNotFound)
		return
	}
	s.firmwareMu.Lock()
	defer s.firmwareMu.Unlock()
	var p firmware.Package
	if _, err := s.db.Get(firmwareRecordKind, id, &p); err != nil {
		s.storeError(w, err)
		return
	}
	if r.Method == http.MethodDelete {
		lock, err := s.lockFirmwareCache()
		if err != nil {
			s.firmwareError(w, err)
			return
		}
		defer lock.Close()
		// Removing the cache entry is safe even when its bytes failed readback;
		// this is the recovery path for a missing or damaged download.
		err = os.Remove(filepath.Join(s.firmwareCacheDir(), id+".zip"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			s.internal(w, err)
			return
		}
		if err := s.db.Delete(firmwareRecordKind, id); err != nil {
			s.storeError(w, err)
			return
		}
		s.json(w, 200, map[string]bool{"deleted": true})
		return
	}
	s.json(w, 200, p)
}

func (s *Server) downloadFirmwarePackage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "packageID")
	if !firmware.ValidID(id) {
		s.storeError(w, store.ErrNotFound)
		return
	}
	s.firmwareMu.Lock()
	var p firmware.Package
	_, err := s.db.Get(firmwareRecordKind, id, &p)
	var archive []byte
	if err == nil {
		archive, err = s.readFirmwareArchive(id)
	}
	s.firmwareMu.Unlock()
	if err != nil {
		s.firmwareError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="picobrew-firmware-%s.zip"`, id[:12]))
	http.ServeContent(w, r, id+".zip", p.CreatedAt, bytes.NewReader(archive))
}

func (s *Server) machineFirmware(w http.ResponseWriter, r *http.Request) {
	d, ok := s.lookupRuntime(w, r)
	if !ok {
		return
	}
	out := firmware.MachineFirmware{
		Packages: []firmware.Package{},
		Reason:   "Connect and identify a brewer to check wireless update availability.",
	}
	d.mu.RLock()
	out.DeviceID = d.view.ID
	if d.view.Adapter == "simulator" {
		out.Reason = "Simulators do not receive firmware."
	} else if !d.identityMismatch && d.view.Capabilities != nil {
		out.CurrentVersion = d.view.Capabilities.FirmwareVersion
		_, modelError := device.ReportedModel(*d.view.Capabilities)
		if d.view.ModelSource == "firmware" && d.view.ModelError == "" &&
			modelError == nil && device.SupportedModel(d.view.Capabilities.Model) && d.view.Status != nil && validateStatusModel(*d.view.Status, *d.view.Capabilities) == nil {
			out.Model = d.view.Capabilities.Model
			out.DisplayVariant = d.view.Capabilities.DisplayVariant
			out.DeviceUID = d.view.Status.DeviceUID
			out.InstallSupported = !s.cfg.Demo && otaEligible(d.view)
			switch {
			case s.cfg.Demo:
				out.Reason = "Firmware installation is unavailable in simulation."
			case !firmware.ValidModel(out.Model) && firmware.FactoryApplicationLimit(out.Model) == 0 && out.Model != "picoferm":
				out.Reason = "Hub wireless firmware installation is unavailable for this model."
			case out.InstallSupported:
				out.Reason = "The Hub can install a matching OTA package after the brewer verifies its recovery image and boot layout."
			case !reportsWirelessUpdate(*d.view.Capabilities):
				out.Reason = "Wireless installation requires a USB setup with compatible firmware and recovery support."
			default:
				out.Reason = "Wireless installation requires an enabled, connected, idle brewer with its own device key."
			}
		}
	}
	retryEnabled := d.view.Enabled && !d.view.KeyReused && d.view.Adapter == "pico-standalone" && (!d.identityMismatch || d.awaitingIdentity) && (!d.view.Online || out.InstallSupported)
	active := d.updatingFirmware
	stockCapable := d.view.Capabilities != nil && d.view.Capabilities.StockRestoreOTAV1
	stockRetry := !s.cfg.Demo && d.view.Adapter == "pico-standalone" && d.view.Enabled && !d.view.KeyReused && (!d.identityMismatch || d.awaitingIdentity)

	// A previously identified offline machine can still use its local USB guide.
	// The guide never enables a wireless install or initiates a hardware write.
	restoreModel := out.Model
	if restoreModel == "" && d.awaitingIdentity && !d.view.Online && d.view.ModelSource == "firmware" {
		restoreModel = d.view.Model
	}
	canShowRestore := !s.cfg.Demo && !active && (!d.identityMismatch || d.awaitingIdentity) &&
		!d.view.KeyReused && d.view.ModelError == "" && d.view.Adapter == "pico-standalone" && device.SupportedModel(restoreModel)
	d.mu.RUnlock()
	if canShowRestore {
		var pin deviceIdentity
		_, err := s.db.Get("device-identity", out.DeviceID, &pin)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.storeError(w, err)
			return
		}
		if pin.UID != "" && (out.DeviceUID == pin.UID || out.Model == "") {
			out.StockRestoreGuide = true
			out.USBRestoreTarget = &firmware.USBRestoreTarget{Model: restoreModel, UID: pin.UID}
		}
	}
	var receipt firmware.InstallReceipt
	if _, err := s.db.Get("firmware-install", out.DeviceID, &receipt); err == nil {
		if receipt.State == "running" && !active {
			receipt.State = "interrupted"
			receipt.Message = "Hub restarted before it recorded the result. Check the brewer before continuing."
		}
		out.LastInstall = &receipt
		if !active && retryEnabled && canResumeFirmware(receipt) && (!out.InstallSupported || pendingFirmwareConflict(receipt) != "") && (out.Model == "" || out.Model == receipt.Model && out.DeviceUID == receipt.UID && out.DisplayVariant == receipt.DisplayVariant) && !s.cfg.Demo {
			matches, err := s.savedFirmwareIdentity(out.DeviceID, receipt.UID)
			if err != nil {
				s.storeError(w, err)
				return
			}
			out.InstallSupported = matches
			if matches {
				out.Model = receipt.Model
				out.DisplayVariant = receipt.DisplayVariant
				out.DeviceUID = receipt.UID
				out.RetryPackageID = receipt.PackageID
				out.Reason = "Retry the same firmware package so the Hub can verify the interrupted update."
			}
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		s.storeError(w, err)
		return
	}
	stock, _, err := s.stockReceipt(out.DeviceID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	if stock != nil {
		if stock.State == "running" && !active {
			stock.State = "interrupted"
			stock.Message = stockIssue(stock.Stage, "The Hub restarted before it recorded the result.")
		}
		out.LastStockRestore = stock
		if canObserveStock(stock) && !active && stockRetry {
			match, err := s.savedFirmwareIdentity(out.DeviceID, stock.UID)
			if err != nil {
				s.storeError(w, err)
				return
			}
			out.CanConfirmStockStartup = match && (out.DeviceUID == "" || out.DeviceUID == stock.UID)
		}
	}
	if out.USBRestoreTarget != nil && stockfirmware.WirelessProfile(out.USBRestoreTarget.Model, out.USBRestoreTarget.UID) {
		out.StockRestoreWireless = out.InstallSupported && stockCapable && out.RetryPackageID == "" && !active
		out.StockRestoreUpgrade = out.InstallSupported && !stockCapable && stockUpgradeAvailable(out.CurrentVersion) && out.RetryPackageID == "" && !active
	}
	if stockPending(stock) {
		// The brewer can remain on custom firmware after a failed preflight,
		// or return to it through USB. A new installation checks the live boot.
		out.InstallSupported = out.InstallSupported && out.Model == stock.Model && out.DeviceUID == stock.UID
		out.RetryPackageID = ""
		out.StockRestoreWireless = false
		out.StockRestoreUpgrade = false
		if stockRetry && !active && (out.DeviceUID == "" || out.DeviceUID == stock.UID) {
			match, err := s.savedFirmwareIdentity(out.DeviceID, stock.UID)
			if err != nil {
				s.storeError(w, err)
				return
			}
			out.StockRestoreWireless = match && stockfirmware.WirelessProfile(stock.Model, stock.UID)
		}
	}
	if active {
		out.InstallSupported = false
		out.Reason = "Firmware update in progress."
	}
	if out.Model == "pico-c" {
		verified, err := s.verifiedFirmwareDisplay(out.DeviceID, out.DeviceUID)
		if err != nil {
			s.storeError(w, err)
			return
		}
		out.VerifiedDisplayVariant = verified
		out.HardwareVerificationRequired = verified == "" || verified != out.DisplayVariant
		if out.HardwareVerificationRequired {
			out.InstallSupported = false
		}
		if verified == "" {
			out.Reason += " Record the display controller fitted to this Pico C."
		} else if verified != out.DisplayVariant {
			out.Reason += " The firmware-reported display differs from the independently verified controller."
		}
	}
	if out.Model != "" {
		catalog, err := s.savedCatalog()
		if err != nil {
			s.firmwareError(w, err)
			return
		}
		s.firmwareMu.Lock()
		packages, err := s.listFirmwarePackages()
		s.firmwareMu.Unlock()
		if err != nil {
			s.firmwareError(w, err)
			return
		}
		for _, p := range packages {
			if p.ID != out.RetryPackageID && catalog.Withdrawn(p) {
				continue
			}
			if p.MatchesWirelessTarget(out.Model, out.DisplayVariant) {
				out.Packages = append(out.Packages, p)
			}
		}
	}
	s.json(w, 200, out)
}
