package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/firmware"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

func reportsWirelessUpdate(c domain.Capabilities) bool {
	switch c.Model {
	case "picostill", "z-series":
		return c.OTAProtocol == firmware.ESP32FactoryProtocol
	case "picoferm":
		return c.OTAProtocol == firmware.FermABProtocol
	default:
		return c.HubOTAV1 && c.OTAProtocol == "" && firmware.ValidVariant(c.Model, c.DisplayVariant)
	}
}

func otaEligible(v domain.DeviceView) bool {
	return v.Adapter == "pico-standalone" && v.Enabled && v.Online && !v.KeyReused &&
		v.ModelSource == "firmware" && v.ModelError == "" && v.Capabilities != nil &&
		v.Capabilities.Model == v.Model &&
		reportsWirelessUpdate(*v.Capabilities) &&
		v.Status != nil && v.Status.Ready && !v.Status.Busy && !v.Status.Stopped && v.Status.DeviceUID != ""
}

// Each checkpoint precedes a device mutation. The previous package owns the
// transfer or trial even if the device still answers its normal status API.
func pendingFirmwareConflict(receipt firmware.InstallReceipt) string {
	if receipt.State != "installed" {
		switch receipt.Stage {
		case "entering", "transferring":
			return "transfer_pending"
		case "selecting", "confirming":
			return "trial_pending"
		}
	}
	return ""
}

func (s *Server) savedFirmwareIdentity(id, uid string) (bool, error) {
	var pin deviceIdentity
	_, err := s.db.Get("device-identity", id, &pin)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil && uid != "" && pin.UID == uid, err
}

func (s *Server) installFirmware(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Demo {
		s.fail(w, 409, "demo_device", "Firmware installation requires a physical brewer")
		return
	}
	var in struct {
		PackageID string `json:"package_id"`
		UID       string `json:"device_uid"`
		Confirm   bool   `json:"confirm"`
	}
	if !s.decode(w, r, &in) {
		return
	}
	if !firmware.ValidID(in.PackageID) || !in.Confirm || in.UID == "" {
		s.fail(w, 400, "invalid_install", "Confirm the selected package and brewer")
		return
	}
	d, ok := s.lookupRuntime(w, r)
	if !ok {
		return
	}
	updater, ok := d.adapter.(device.FirmwareUpdater)
	if !ok {
		s.fail(w, 409, "unsupported_firmware", "This adapter cannot install firmware")
		return
	}
	// Never queue a second installation behind another update.
	d.mu.Lock()
	if d.updatingFirmware {
		d.mu.Unlock()
		s.fail(w, 409, "update_running", "A firmware update is already in progress")
		return
	}
	d.updatingFirmware = true
	d.mu.Unlock()
	release := func() { d.mu.Lock(); d.updatingFirmware = false; d.mu.Unlock(); d.commandMu.Unlock() }
	d.commandMu.Lock()
	dispatched := false
	defer func() {
		if !dispatched {
			release()
		}
	}()
	if !s.registeredDevice(d) {
		s.fail(w, 409, "device_changed", "The registration changed")
		return
	}
	d.mu.RLock()
	view := d.view
	mismatch, awaitingIdentity := d.identityMismatch, d.awaitingIdentity
	d.mu.RUnlock()
	if !view.Enabled || view.KeyReused || view.Adapter != "pico-standalone" {
		s.fail(w, 409, "ota_unavailable", "Use an enabled physical brewer with a unique key")
		return
	}
	stock, stockVersion, err := s.stockReceipt(view.ID)
	if err != nil {
		s.storeError(w, err)
		return
	}
	var prior firmware.InstallReceipt
	priorVersion, err := s.db.Get("firmware-install", view.ID, &prior)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.storeError(w, err)
		return
	}
	resume := prior.PackageID == in.PackageID && prior.UID == in.UID && prior.State != "installed"
	if mismatch && !(resume && awaitingIdentity) {
		s.fail(w, 409, "ota_unavailable", "The registered brewer identity could not be verified")
		return
	}
	if conflict := pendingFirmwareConflict(prior); conflict != "" && !resume {
		s.fail(w, 409, conflict, "Finish the interrupted firmware update before installing another version")
		return
	}
	model := prior.Model
	display := prior.DisplayVariant
	var currentVersion string
	if resume {
		if view.Online && (!otaEligible(view) || view.Status.DeviceUID != prior.UID ||
			view.Model != prior.Model || view.Capabilities.DisplayVariant != prior.DisplayVariant) {
			s.fail(w, 409, "ota_unavailable", "Retry on the same idle brewer with wireless update support")
			return
		}
		// A restarted Hub can find the brewer in recovery, which has no normal
		// status endpoint. Require the original persisted pin; the adapter
		// independently verifies the live application or recovery identity.
		matches, err := s.savedFirmwareIdentity(view.ID, in.UID)
		if err != nil {
			s.storeError(w, err)
			return
		}
		if !matches {
			s.fail(w, 409, "identity_mismatch", "The update does not match the registered brewer identity")
			return
		}
	} else {
		status, err := s.verifiedStatus(r.Context(), d)
		if err != nil {
			s.deviceError(w, err)
			return
		}
		d.mu.RLock()
		ready := otaEligible(d.view)
		if ready {
			currentVersion = d.view.Capabilities.FirmwareVersion
		}
		d.mu.RUnlock()
		if !ready || status.DeviceUID != in.UID {
			s.fail(w, 409, "ota_unavailable", "Use an idle brewer with wireless update support and its verified device identity")
			return
		}
		model = status.Model
		display = status.DisplayVariant
		if currentVersion == "" {
			s.fail(w, 409, "firmware_version_unknown", "The brewer did not report its installed firmware version")
			return
		}
	}
	if model == "pico-c" {
		verified, err := s.verifiedFirmwareDisplay(view.ID, in.UID)
		if err != nil {
			s.storeError(w, err)
			return
		}
		if !firmware.ValidVariant(model, display) || verified != display {
			s.fail(w, 409, "hardware_verification", "Verify the display controller fitted to this Pico C before installing firmware")
			return
		}
	}
	var saved firmware.Package
	s.firmwareMu.Lock()
	_, err = s.db.Get(firmwareRecordKind, in.PackageID, &saved)
	var archive []byte
	if err == nil {
		archive, err = s.readFirmwareArchive(in.PackageID)
	}
	s.firmwareMu.Unlock()
	if err != nil {
		s.firmwareError(w, err)
		return
	}
	if !resume {
		catalog, err := s.savedCatalog()
		if err != nil {
			s.firmwareError(w, err)
			return
		}
		if catalog.Withdrawn(saved) {
			s.fail(w, 409, "firmware_withdrawn", "Choose a firmware version currently offered by the configured release source")
			return
		}
	}
	var payload firmware.UpdatePayload
	if saved.SupportsFermOTA() {
		payload, err = firmware.ReadPicoFermUpdate(archive, saved.Candidate)
	} else if saved.SupportsFactoryOTA() {
		payload, err = firmware.ReadFactoryUpdate(archive, saved.Candidate)
	} else {
		payload, err = firmware.ReadUpdatePayload(archive)
	}
	if err != nil {
		s.firmwareError(w, err)
		return
	}
	if !payload.MatchesWirelessTarget(model, display) {
		s.fail(w, 409, "firmware_model", "The package model does not match this brewer")
		return
	}
	if resume {
		payload.ReplacesPackageID = prior.ReplacesPackageID
	} else if currentVersion == payload.FirmwareVersion {
		if in.PackageID != prior.RevisedPackageID(in.UID, model, display, currentVersion) {
			s.fail(w, 409, "firmware_already_installed", "This firmware version is already installed on the brewer")
			return
		}
		payload.ReplacesPackageID = prior.PackageID
	}
	if payload.ReplacesPackageID != "" && !payload.IsApprovedRevision() {
		s.fail(w, 409, "firmware_revision", "This package does not match the approved revision for this machine")
		return
	}
	if stockPending(stock) {
		if err := s.reconcileCustomBoot(r.Context(), d, in.UID, stock, stockVersion); err != nil {
			s.fail(w, 409, "stock_restore_pending", "The previous stock restoration is pending and the Hub could not verify a stable custom application on this brewer")
			return
		}
	}
	receipt := firmware.InstallReceipt{ReplacesPackageID: payload.ReplacesPackageID, UID: in.UID, Model: model, DisplayVariant: display, PackageID: in.PackageID, Started: time.Now().UTC(), State: "running", Message: "Checking recovery, transferring the package and confirming the new boot."}
	previousStage := ""
	if resume {
		previousStage = prior.Stage
		if previousStage == "" && payload.ReplacesPackageID == "" {
			previousStage = "transferring"
		}
	}
	receipt.Stage = previousStage
	version, err := s.db.Put("firmware-install", view.ID, receipt, priorVersion)
	if err != nil {
		s.storeError(w, err)
		return
	}
	dispatched = true
	s.wg.Add(1)
	go func(result firmware.InstallReceipt) {
		defer s.wg.Done()
		defer release()
		ctx, cancel := context.WithTimeout(s.ctx, 12*time.Minute)
		defer cancel()
		checkpoint := func(stage string) error {
			result.Stage = stage
			next, err := s.db.Put("firmware-install", view.ID, result, version)
			if err == nil {
				version = next
			}
			return err
		}
		if err := updater.InstallFirmware(ctx, in.UID, payload, previousStage, checkpoint); err != nil {
			result.State = "unconfirmed"
			result.Message = safeMessage(err.Error())
		} else {
			result.State = "installed"
			result.Message = "The brewer verified the installed package and confirmed its new boot selection."
		}
		if _, err := s.db.Put("firmware-install", view.ID, result, version); err != nil {
			s.cfg.Logger.Error("Could not record firmware installation result", "device", view.ID, "error", err)
		}
	}(receipt)
	s.json(w, 202, receipt)
}
