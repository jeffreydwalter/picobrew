package device

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strconv"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/firmware"
)

const espUpdater = "/api/v1/firmware/ota"

type espBootInfo struct {
	Model         string `json:"model"`
	UID           string `json:"device_uid"`
	Protocol      string `json:"protocol"`
	Mode          string `json:"mode"`
	Version       string `json:"version"`
	Capacity      int    `json:"max_bytes"`
	RunningSHA    string `json:"image_sha256"`
	RunningSize   int    `json:"image_size"`
	BootstrapSHA  string `json:"bootstrap_sha256"`
	RunningBank   *int   `json:"running_bank,omitempty"`
	TargetBank    *int   `json:"target_bank,omitempty"`
	Trial         *bool  `json:"trial,omitempty"`
	SHA           string `json:"sha256"`
	TargetVersion string `json:"target_version"`
	Size          int    `json:"size"`
	espTransfer
}

type espTransfer struct {
	Session espSession `json:"session"`
	Offset  int        `json:"offset"`
}

// PicoStill issues a 32-bit number. PicoFerm issues a 16-digit hex string.
// Preserve the JSON type when acknowledging or resuming a device session.
type espSession string

func (s *espSession) UnmarshalJSON(raw []byte) error {
	switch string(raw) {
	case "0", `""`, "null":
		*s = ""
	default:
		*s = espSession(raw)
	}
	return nil
}

func (s espSession) MarshalJSON() ([]byte, error) {
	if s == "" {
		return []byte("null"), nil
	}
	return []byte(s), nil
}

func (s espSession) valid(protocol string) bool {
	if protocol == firmware.FermABProtocol {
		var id string
		if json.Unmarshal([]byte(s), &id) != nil || len(id) != 16 {
			return false
		}
		_, err := hex.DecodeString(id)
		return err == nil
	}
	id, err := strconv.ParseUint(string(s), 10, 32)
	return err == nil && id != 0
}

func (p *pico) inspectESP(ctx context.Context, uid string, pkg firmware.UpdatePayload) (espBootInfo, error) {
	var info espBootInfo
	if err := p.request(ctx, "GET", espUpdater, nil, &info, 200); err != nil {
		return info, err
	}
	if info.UID != uid || info.Model != pkg.Model || info.Protocol != pkg.OTAProtocol || (info.Mode != "application" && info.Mode != "recovery") {
		return info, otaFailure("The device did not confirm its identity and update protocol")
	}
	if pkg.PicoFerm != nil {
		if info.Capacity != firmware.FermBankLimit || info.BootstrapSHA != pkg.PicoFerm.BootstrapSHA256 ||
			!firmware.ValidID(info.BootstrapSHA) || info.RunningBank == nil || info.TargetBank == nil || info.Trial == nil ||
			*info.RunningBank < 0 || *info.RunningBank > 1 || *info.TargetBank != 1-*info.RunningBank {
			return info, otaFailure("PicoFerm did not confirm the expected bootloader and application banks")
		}
	} else if info.Capacity != firmware.FactoryApplicationLimit(pkg.Model) {
		return info, otaFailure("Recovery did not confirm the expected application partition size")
	}
	return info, nil
}

type espAppImage struct {
	data []byte
	hash string
	bank int
}

func espImage(pkg firmware.UpdatePayload, bank *int) (espAppImage, error) {
	if pkg.SupportsFermOTA() && pkg.PicoFerm != nil && bank != nil && *bank >= 0 && *bank <= 1 {
		b := pkg.PicoFerm.Banks[*bank]
		if b.Bank == *bank && b.Size > 0 && b.Size <= firmware.FermBankLimit && len(b.Image) == b.Size && firmware.Digest(b.Image) == b.SHA256 {
			return espAppImage{b.Image, b.SHA256, b.Bank}, nil
		}
	} else if pkg.SupportsFactoryOTA() && pkg.PicoFerm == nil && len(pkg.Files[0]) == pkg.BinaryBytes && firmware.Digest(pkg.Files[0]) == pkg.BinarySHA256 {
		return espAppImage{pkg.Files[0], pkg.BinarySHA256, -1}, nil
	}
	return espAppImage{}, otaFailure("The wireless application did not match the selected device package")
}

func (app espAppImage) running(info espBootInfo, version string) bool {
	return info.Mode == "application" && info.Version == version && info.RunningSHA == app.hash && info.RunningSize == len(app.data) &&
		(app.bank == -1 || info.RunningBank != nil && *info.RunningBank == app.bank)
}

func (p *pico) installESP(ctx context.Context, uid string, pkg firmware.UpdatePayload, prior string, checkpoint func(string) error) error {
	if uid == "" || (!pkg.SupportsFactoryOTA() && !pkg.SupportsFermOTA()) || (pkg.PicoFerm != nil) != pkg.SupportsFermOTA() {
		return otaFailure("Invalid target for this wireless firmware package")
	}
	info, err := p.inspectESP(ctx, uid, pkg)
	if err != nil {
		return err
	}
	if info.Mode == "application" && info.Version == pkg.FirmwareVersion {
		app, err := espImage(pkg, info.RunningBank)
		if err != nil || (prior != "selecting" && prior != "confirming") || !app.running(info, pkg.FirmwareVersion) {
			return otaFailure("The device already reports this version or a different image with the same version")
		}
		return p.confirmESP(ctx, uid, pkg, app, info)
	}
	app, err := espImage(pkg, info.TargetBank)
	if err != nil {
		return err
	}
	if info.Mode == "application" {
		if info.Trial != nil && *info.Trial {
			return otaFailure("Resolve the current trial boot before installing another package")
		}
		if err := checkpoint("entering"); err != nil {
			return err
		}
		if err := p.request(ctx, "POST", espUpdater+"/enter", map[string]string{"device_uid": uid}, nil, 202); err != nil {
			return err
		}
		if err := awaitBoot(ctx, func(c context.Context) bool {
			var e error
			info, e = p.inspectESP(c, uid, pkg)
			return e == nil && info.Mode == "recovery" && (app.bank == -1 || *info.TargetBank == app.bank)
		}); err != nil {
			return err
		}
	} else if prior == "" {
		return otaFailure("This recovery session was not started by this installation")
	}
	if err := checkpoint("transferring"); err != nil {
		return err
	}
	transfer := info.espTransfer
	if transfer.Session == "" {
		body := map[string]any{"device_uid": uid, "model": pkg.Model, "version": pkg.FirmwareVersion, "sha256": app.hash, "size": len(app.data)}
		if err := p.request(ctx, "POST", espUpdater+"/begin", body, &transfer, 200); err != nil {
			return err
		}
		if !transfer.Session.valid(pkg.OTAProtocol) || transfer.Offset != 0 {
			return otaFailure("Recovery did not open an empty upload")
		}
	} else if !transfer.Session.valid(pkg.OTAProtocol) || info.TargetVersion != pkg.FirmwareVersion || info.SHA != app.hash || info.Size != len(app.data) || transfer.Offset < 0 || transfer.Offset > len(app.data) {
		return otaFailure("Recovery holds a different or invalid upload")
	}
	for transfer.Offset < len(app.data) {
		start := transfer.Offset
		end := min(start+1024, len(app.data))
		body := map[string]any{"session": transfer.Session, "offset": start, "data": hex.EncodeToString(app.data[start:end])}
		var ack espTransfer
		if err := p.request(ctx, "POST", espUpdater+"/chunk", body, &ack, 200); err != nil {
			return err
		}
		if ack.Session != transfer.Session || ack.Offset != end {
			return otaFailure("Recovery did not acknowledge the expected application offset")
		}
		transfer = ack
	}
	if err := checkpoint("selecting"); err != nil {
		return err
	}
	var ack espTransfer
	if err := p.request(ctx, "POST", espUpdater+"/finish", map[string]espSession{"session": transfer.Session}, &ack, 202); err != nil {
		return err
	}
	if ack != transfer {
		return otaFailure("Recovery did not confirm the completed application image")
	}
	if err := checkpoint("confirming"); err != nil {
		return err
	}
	if err := awaitBoot(ctx, func(c context.Context) bool {
		var e error
		info, e = p.inspectESP(c, uid, pkg)
		return e == nil && app.running(info, pkg.FirmwareVersion)
	}); err != nil {
		return err
	}
	return p.confirmESP(ctx, uid, pkg, app, info)
}

func (p *pico) confirmESP(ctx context.Context, uid string, pkg firmware.UpdatePayload, app espAppImage, info espBootInfo) error {
	if info.Trial == nil || !*info.Trial {
		return nil
	}
	if err := p.request(ctx, "POST", espUpdater+"/confirm", map[string]string{"device_uid": uid, "version": pkg.FirmwareVersion}, nil, 200); err != nil {
		return err
	}
	state, err := p.inspectESP(ctx, uid, pkg)
	if err != nil {
		return err
	}
	if !app.running(state, pkg.FirmwareVersion) || state.Trial == nil || *state.Trial {
		return otaFailure("PicoFerm did not confirm the verified application bank")
	}
	return nil
}
