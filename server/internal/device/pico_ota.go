package device

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/firmware"
)

type FirmwareUpdater interface {
	InstallFirmware(context.Context, string, firmware.UpdatePayload, string, func(string) error) error
}

type otaIdentity struct {
	DisplayVariant string `json:"display_variant,omitempty"`
	UID            string `json:"device_uid"`
	Model          string `json:"model"`
	Digest         string `json:"sha256"`
}
type otaState struct {
	otaIdentity
	Size  int  `json:"size"`
	Trial bool `json:"trial"`
	OK    bool `json:"ok"`
}
type recoveryAck struct {
	OK     bool   `json:"ok"`
	ID     uint32 `json:"id"`
	Offset int    `json:"next_offset"`
	Mask   uint32 `json:"verified_mask"`
	Boot   bool   `json:"boot_committed"`
}

func otaFailure(message string) error {
	return &Error{StatusCode: 409, Code: "firmware_update", Message: message}
}

func (p *pico) recoveryPost(ctx context.Context, operation string, id uint32, body map[string]any) (recoveryAck, error) {
	var ack recoveryAck
	if id != 0 {
		body["id"] = id
	}
	err := p.request(ctx, "POST", "/api/v1/recovery/"+operation, body, &ack, 200)
	if err == nil && (!ack.OK || ack.ID == 0 || id != 0 && ack.ID != id || operation != "select" && ack.Boot) {
		err = otaFailure("Recovery returned an unexpected session acknowledgement")
	}
	return ack, err
}

// Only observations may be retried after a restart. Each upload and boot mutation is sent once.
func awaitBoot(ctx context.Context, observe func(context.Context) bool) error {
	wait, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	for {
		if observe(wait) {
			return nil
		}
		select {
		case <-wait.Done():
			return otaFailure("The brewer did not confirm the expected boot state. Check its display or local recovery page before retrying")
		case <-time.After(time.Second):
		}
	}
}

func (p *pico) InstallFirmware(ctx context.Context, uid string, pkg firmware.UpdatePayload, previousStage string, checkpoint func(string) error) error {
	if err := p.lock(ctx); err != nil {
		return err
	}
	defer p.unlock()
	// Long flash readbacks need more time than the usual polling deadline.
	remote := &pico{base: p.base, key: p.key, client: &http.Client{Transport: p.client.Transport, CheckRedirect: p.client.CheckRedirect, Timeout: 30 * time.Second}}
	if pkg.ReplacesPackageID != "" && !pkg.IsApprovedRevision() {
		return otaFailure("Invalid revised firmware package")
	}
	if pkg.OTAProtocol != "" {
		return remote.installESP(ctx, uid, pkg, previousStage, checkpoint)
	}
	var before otaState
	inspectErr := remote.request(ctx, "GET", "/api/v1/ota", nil, &before, 200)
	if inspectErr != nil && previousStage == "" {
		return inspectErr
	}
	if inspectErr == nil {
		if !before.OK || before.UID != uid || before.Model != pkg.Model || before.DisplayVariant != pkg.DisplayVariant || !firmware.ValidID(before.Digest) || before.Size <= 0 || before.Size > firmware.MaxBinaryBytes {
			return otaFailure("The update preflight could not verify this brewer and its installed image")
		}
		if before.Trial && (before.Digest != pkg.BinarySHA256 || (previousStage != "selecting" && previousStage != "confirming")) {
			return otaFailure("Confirm the currently running trial package before starting another update")
		}
		if !before.Trial && previousStage == "confirming" && before.Digest == pkg.BinarySHA256 {
			// A lost confirmation reply can be reconciled without writing again.
			return remote.awaitApplication(ctx, before.otaIdentity, pkg.FirmwareVersion, false)
		}
		if !before.Trial {
			// A retry can find an ordinary application after recovery. Verify its
			// current version before requesting another reboot or writing flash.
			var caps domain.Capabilities
			if err := remote.request(ctx, "GET", "/api/v1", nil, &caps, 200); err != nil {
				return err
			}
			if !caps.HubOTAV1 || caps.Model != pkg.Model || caps.DisplayVariant != pkg.DisplayVariant || caps.FirmwareVersion == "" {
				return otaFailure("The brewer did not confirm its model, installed version and OTA support")
			}
			if pkg.ReplacesPackageID != "" {
				if caps.FirmwareVersion != pkg.FirmwareVersion || !pkg.ReplacesRunningImage(before.Digest) {
					return otaFailure("The running application does not match the recorded build for this revision")
				}
				// Only after verifying the source image may a retry proceed directly in recovery.
				if err := checkpoint("entering"); err != nil {
					return err
				}
			} else if caps.FirmwareVersion == pkg.FirmwareVersion {
				return &Error{StatusCode: 409, Code: "firmware_already_installed", Message: "This firmware version is already installed on the brewer"}
			}
			var entered otaState
			if err := remote.request(ctx, "POST", "/api/v1/ota/enter", before.otaIdentity, &entered, 200); err != nil {
				return err
			}
			if !entered.OK || entered.otaIdentity != before.otaIdentity {
				return otaFailure("Recovery entry was not acknowledged by the expected brewer")
			}
		}
	}
	if !before.Trial {
		if err := awaitBoot(ctx, func(c context.Context) bool {
			info, ready := remote.readRecovery(c, uid, pkg.DisplayVariant, 2, pkg.BinaryBytes)
			return ready && !info.Upload.Active
		}); err != nil {
			return err
		}
		if err := remote.transferFirmware(ctx, pkg, checkpoint); err != nil {
			return err
		}
	}
	target := otaIdentity{UID: uid, Model: pkg.Model, DisplayVariant: pkg.DisplayVariant, Digest: pkg.BinarySHA256}
	if err := remote.awaitApplication(ctx, target, pkg.FirmwareVersion, true); err != nil {
		return err
	}
	if err := checkpoint("confirming"); err != nil {
		return err
	}
	var confirmed otaState
	if err := remote.request(ctx, "POST", "/api/v1/ota/confirm", target, &confirmed, 200); err != nil {
		return err
	}
	if !confirmed.OK || confirmed.Trial || confirmed.otaIdentity != target {
		return otaFailure("The new firmware did not confirm its permanent boot selection")
	}
	return nil
}

func (remote *pico) awaitApplication(ctx context.Context, target otaIdentity, version string, trial bool) error {
	return awaitBoot(ctx, func(c context.Context) bool {
		var s domain.Status
		if remote.request(c, "GET", "/api/v1/status", nil, &s, 200) != nil || s.DeviceUID != target.UID || s.Model != target.Model || s.DisplayVariant != target.DisplayVariant || !s.Ready || s.Busy || s.Stopped {
			return false
		}
		var caps domain.Capabilities
		if remote.request(c, "GET", "/api/v1", nil, &caps, 200) != nil || !caps.HubOTAV1 || caps.Model != target.Model || caps.DisplayVariant != target.DisplayVariant || caps.FirmwareVersion != version {
			return false
		}
		var state otaState
		return remote.request(c, "GET", "/api/v1/ota", nil, &state, 200) == nil && state.OK && state.Trial == trial && state.otaIdentity == target
	})
}

func (p *pico) transferFirmware(ctx context.Context, pkg firmware.UpdatePayload, checkpoint func(string) error) error {
	plan := map[string]any{"slot": 2, "web": true, "version": pkg.FirmwareVersion, "size": pkg.BinaryBytes, "sha256": pkg.BinarySHA256}
	if pkg.DisplayVariant != "" {
		plan["display_variant"] = pkg.DisplayVariant
	}
	for i, name := range []string{"index", "app", "explorer", "history", "style"} {
		data := pkg.Files[i+1]
		plan[name+"_size"] = len(data)
		plan[name+"_sha256"] = firmware.Digest(data)
	}
	session, err := p.recoveryPost(ctx, "plan", 0, plan)
	if err != nil {
		return err
	}
	for target, data := range pkg.Files {
		if err := p.transferRecoveryFile(ctx, session.ID, target, data); err != nil {
			return err
		}
	}
	if err := checkpoint("selecting"); err != nil {
		return err
	}
	ack, err := p.recoveryPost(ctx, "select", session.ID, map[string]any{"trial": true})
	if err != nil {
		return err
	}
	if !ack.Boot || ack.Mask != 63 {
		return otaFailure("Recovery did not confirm the complete package and trial selection")
	}
	return nil
}

type recoveryInventory struct {
	Profile, Version string
	Boot             struct {
		Valid            bool
		RecoverySelected bool `json:"recovery_selected"`
	}
	Slots []struct {
		Slot, Allocated int
		Present         bool
	}
	Upload struct {
		Active bool
		ID     uint32
		Target int
	}
	Plan struct {
		Active  bool
		ID      uint32
		Slot    int
		Version string
		Files   []struct {
			Target, Size int
			SHA256       string
		}
	}
}

func (remote *pico) readRecovery(c context.Context, uid, displayVariant string, slot, size int) (recoveryInventory, bool) {
	var identity struct {
		UID            string `json:"device_uid"`
		Protocol       int    `json:"hub_ota_protocol"`
		Model          string `json:"model"`
		DisplayVariant string `json:"display_variant"`
	}
	var info recoveryInventory
	if remote.request(c, "GET", "/api/v1/recovery/identity", nil, &identity, 200) != nil || identity.UID != uid || identity.Protocol != 1 {
		return info, false
	}
	if identity.DisplayVariant != displayVariant || (displayVariant != "" && identity.Model != "pico-c") || (identity.Model == "pico-c" && displayVariant == "") {
		return info, false
	}
	if remote.request(c, "GET", "/api/v1/recovery", nil, &info, 200) != nil || info.Profile != "recovery" ||
		(info.Version != "1.0.0" && info.Version != "0.1.0") || !info.Boot.Valid || !info.Boot.RecoverySelected {
		return info, false
	}
	for _, s := range info.Slots {
		if s.Slot == slot && s.Present && s.Allocated >= size {
			return info, true
		}
	}
	return info, false
}

func (p *pico) transferRecoveryFile(ctx context.Context, sessionID uint32, target int, data []byte) error {
	args := map[string]any{"target": target}
	checked, err := p.recoveryPost(ctx, "verify", sessionID, args)
	if err == nil {
		if checked.Mask&(1<<target) == 0 {
			return otaFailure("Recovery did not acknowledge file verification")
		}
		return nil
	}
	// A content/size mismatch or absent file is expected for a new version.
	// Transport, session and storage failures have uncertain outcomes.
	if err != nil {
		var problem *Error
		if !errors.As(err, &problem) || (problem.Code != "checksum" && problem.Code != "content" && problem.Code != "incomplete" && problem.Code != "not_found") {
			return err
		}
	}
	if start, err := p.recoveryPost(ctx, "begin", sessionID, args); err != nil {
		return err
	} else if start.Offset != 0 {
		return otaFailure("Recovery did not open the file at offset zero")
	}
	for offset := 0; offset < len(data); {
		end := min(offset+1024, len(data))
		ack, err := p.recoveryPost(ctx, "chunk", sessionID, map[string]any{"offset": offset, "data": hex.EncodeToString(data[offset:end])})
		if err != nil {
			return err
		}
		if ack.Offset != end {
			return otaFailure("Recovery acknowledged an unexpected file offset")
		}
		offset = end
	}
	ack, err := p.recoveryPost(ctx, "finish", sessionID, map[string]any{})
	if err != nil {
		return err
	}
	if ack.Mask&(1<<target) == 0 {
		return otaFailure("Recovery did not verify the uploaded file")
	}
	return nil
}
