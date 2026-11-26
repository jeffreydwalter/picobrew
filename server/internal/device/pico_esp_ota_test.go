package device

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/firmware"
)

func TestZSameVersionNeverStartsAnotherTransfer(t *testing.T) {
	image := []byte("selected application")
	pkg := firmware.UpdatePayload{Candidate: firmware.Candidate{Model: "z-series", ModelFamily: "z-series", FirmwareVersion: "1.0.0", BinaryBytes: len(image), BinarySHA256: firmware.Digest(image), OTAProtocol: firmware.ESP32FactoryProtocol}}
	pkg.Files[0] = image
	for _, running := range [][]byte{image, []byte("older application")} {
		info := espBootInfo{Model: pkg.Model, UID: "module", Protocol: pkg.OTAProtocol, Mode: "application", Version: pkg.FirmwareVersion,
			Capacity: firmware.FactoryApplicationLimit(pkg.Model), RunningSize: len(running), RunningSHA: firmware.Digest(running)}
		p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" || r.URL.Path != espUpdater {
				t.Errorf("unexpected firmware mutation or request: %s %s", r.Method, r.URL.Path)
			}
			replyJSON(w, 200, info)
		})
		if err := p.InstallFirmware(context.Background(), info.UID, pkg, "", nil); err == nil {
			t.Fatal("same-version install accepted", info.RunningSHA)
		}
	}
}

func TestESPUpdateResumesExactImage(t *testing.T) {
	image := bytes.Repeat([]byte{0xe9, 0x30, 0x31}, 831)
	pkg := firmware.UpdatePayload{Candidate: firmware.Candidate{Model: "picostill", ModelFamily: "picostill", FirmwareVersion: "2.0.0", BinaryBytes: len(image), BinarySHA256: firmware.Digest(image), OTAProtocol: firmware.ESP32FactoryProtocol}}
	pkg.Files[0] = image
	for _, tc := range []struct {
		name                string
		interrupt, wrongPin bool
	}{{"complete", false, false}, {"lost chunk acknowledgment", true, false}, {"wrong device", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			uid := "unit-1"
			info := espBootInfo{Model: "picostill", UID: uid, Protocol: firmware.ESP32FactoryProtocol, Mode: "application", Version: "1.0.0", Capacity: firmware.StillApplicationLimit}
			var written []byte
			writes := 0
			lost := false
			p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					writes++
				}
				switch r.URL.Path {
				case espUpdater:
					replyJSON(w, 200, info)
				case espUpdater + "/enter":
					info.Mode = "recovery"
					replyJSON(w, 202, map[string]bool{"ok": true})
				case espUpdater + "/begin":
					info.espTransfer = espTransfer{Session: "24"}
					info.TargetVersion = pkg.FirmwareVersion
					info.Size = len(image)
					info.SHA = pkg.BinarySHA256
					replyJSON(w, 200, info.espTransfer)
				case espUpdater + "/chunk":
					var c struct {
						Session uint32
						Offset  int
						Data    string
					}
					if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
						t.Error(err)
					}
					chunk, err := hex.DecodeString(c.Data)
					if err != nil || c.Session != 24 || len(written) != c.Offset || len(chunk) > 1024 {
						t.Errorf("bad chunk at %v", c.Offset)
					}
					written = append(written, chunk...)
					info.Offset = len(written)
					if tc.interrupt && !lost {
						lost = true
						http.Error(w, "lost reply", 503)
						return
					}
					replyJSON(w, 200, info.espTransfer)
				case espUpdater + "/finish":
					if !bytes.Equal(written, image) {
						t.Error("incorrect application bytes")
					}
					info.Mode = "application"
					info.Version = pkg.FirmwareVersion
					info.RunningSize = len(written)
					info.RunningSHA = firmware.Digest(written)
					replyJSON(w, 202, info.espTransfer)
				default:
					t.Error("unexpected firmware write path", r.URL.Path)
					http.NotFound(w, r)
				}
			})
			stage := ""
			record := func(s string) error { stage = s; return nil }
			if tc.wrongPin {
				if err := p.InstallFirmware(context.Background(), "other", pkg, stage, record); err == nil || writes != 0 {
					t.Fatal("wrong device accepted")
				}
				return
			}
			err := p.InstallFirmware(context.Background(), uid, pkg, stage, record)
			if tc.interrupt {
				if err == nil || stage != "transferring" {
					t.Fatal("lost reply not reported", err, stage)
				}
				err = p.InstallFirmware(context.Background(), uid, pkg, stage, record)
			}
			if err != nil || stage != "confirming" {
				t.Fatal("install failed", err, stage)
			}
			prior := writes
			if err = p.InstallFirmware(context.Background(), uid, pkg, stage, record); err != nil || writes != prior {
				t.Fatal("confirmed retry was not read-only", err)
			}
		})
	}
}

func TestFermUpdateSelectsInactiveBankAndConfirmsItsHash(t *testing.T) {
	const uid = "001122334455"
	const session = "acdc1592bbee5432"
	pkg := firmware.UpdatePayload{
		Candidate: firmware.Candidate{Model: "picoferm", ModelFamily: "picoferm", FirmwareVersion: "2.0.1", OTAProtocol: firmware.FermABProtocol},
		PicoFerm:  &firmware.PicoFermUpdate{BootstrapSHA256: firmware.Digest([]byte("loader"))},
	}
	for i := range pkg.PicoFerm.Banks {
		data := bytes.Repeat([]byte{byte(i), 0xa5}, 1024+8*i)
		pkg.PicoFerm.Banks[i] = firmware.PicoFermBank{Bank: i, Size: len(data), Image: data, SHA256: firmware.Digest(data)}
	}
	pkg.BinarySHA256 = pkg.PicoFerm.Banks[0].SHA256
	pkg.BinaryBytes = pkg.PicoFerm.Banks[0].Size
	for _, test := range []struct {
		name             string
		bank             int
		loseConfirmation bool
	}{
		{"A to B", 0, false},
		{"B to A after lost confirmation", 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			bank, next, trial := test.bank, 1-test.bank, false
			expected := pkg.PicoFerm.Banks[next]
			state := espBootInfo{Model: pkg.Model, UID: uid, Version: "2.0.0", Protocol: pkg.OTAProtocol,
				Capacity: firmware.FermBankLimit, BootstrapSHA: pkg.PicoFerm.BootstrapSHA256, Mode: "application", RunningBank: &bank, TargetBank: &next, Trial: &trial}
			var flashed []byte
			var writes, confirms int
			p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					writes++
				}
				switch r.URL.Path {
				case espUpdater:
					replyJSON(w, 200, state)
				case espUpdater + "/enter":
					state.Mode = "recovery"
					replyJSON(w, 202, state.espTransfer)
				case espUpdater + "/begin":
					var body struct {
						SHA256 string
						Size   int
					}
					if json.NewDecoder(r.Body).Decode(&body) != nil || body.SHA256 != expected.SHA256 || body.Size != expected.Size {
						t.Error("wrong bank selected")
					}
					state.Session = espSession(`"` + session + `"`)
					replyJSON(w, 200, state.espTransfer)
				case espUpdater + "/chunk":
					var body struct {
						Session string
						Offset  int
						Data    string
					}
					if json.NewDecoder(r.Body).Decode(&body) != nil || body.Session != session || body.Offset != len(flashed) {
						t.Error("invalid chunk session or offset")
					}
					part, err := hex.DecodeString(body.Data)
					if err != nil {
						t.Error(err)
					}
					flashed = append(flashed, part...)
					state.Offset = len(flashed)
					replyJSON(w, 200, state.espTransfer)
				case espUpdater + "/finish":
					if !bytes.Equal(flashed, expected.Image) {
						t.Error("wrong firmware contents")
					}
					bank, next, trial = next, bank, true
					state.Mode = "application"
					state.Version = pkg.FirmwareVersion
					state.RunningSHA = expected.SHA256
					state.RunningSize = expected.Size
					replyJSON(w, 202, state.espTransfer)
				case espUpdater + "/confirm":
					var body map[string]string
					if json.NewDecoder(r.Body).Decode(&body) != nil || body["device_uid"] != uid || body["version"] != pkg.FirmwareVersion || !trial {
						t.Error("unexpected confirmation")
					}
					trial = false
					confirms++
					if test.loseConfirmation {
						http.Error(w, "lost response", 503)
						return
					}
					replyJSON(w, 200, state.espTransfer)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			})
			stage := ""
			checkpoint := func(s string) error { stage = s; return nil }
			err := p.InstallFirmware(context.Background(), uid, pkg, stage, checkpoint)
			if test.loseConfirmation {
				if err == nil || stage != "confirming" {
					t.Fatal("missing confirmation must be retryable", err, stage)
				}
				before := writes
				err = p.InstallFirmware(context.Background(), uid, pkg, stage, checkpoint)
				if before != writes {
					t.Fatal("a confirmed retry rewrote flash")
				}
			}
			if err != nil || trial || bank != expected.Bank || confirms != 1 {
				t.Fatal(err, trial, bank, confirms)
			}

			// An unrelated trial of the same version must never receive confirmation.
			state.RunningSHA = pkg.PicoFerm.Banks[next].SHA256
			trial = true
			before := writes
			if err = p.InstallFirmware(context.Background(), uid, pkg, "confirming", checkpoint); err == nil || writes != before {
				t.Fatal("wrong running image accepted", err)
			}
			state.BootstrapSHA = firmware.Digest([]byte("other loader"))
			state.Version = "1.0.0"
			if err = p.InstallFirmware(context.Background(), uid, pkg, "", checkpoint); err == nil || writes != before {
				t.Fatal("wrong bootstrap accepted", err)
			}
		})
	}
}
