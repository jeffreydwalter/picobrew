package device

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/firmware"
)

func TestMatchingLiveVersionNeverEntersRecovery(t *testing.T) {
	pkg := firmware.UpdatePayload{Candidate: firmware.Candidate{Model: "pico-s", FirmwareVersion: "1.3.3", BinarySHA256: strings.Repeat("b", 64)}}
	identity := otaIdentity{UID: "brewer", Model: pkg.Model, Digest: strings.Repeat("a", 64)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/ota":
			json.NewEncoder(w).Encode(otaState{otaIdentity: identity, Size: 256, OK: true})
		case r.Method == "GET" && r.URL.Path == "/api/v1":
			json.NewEncoder(w).Encode(map[string]any{"model": pkg.Model, "firmware_version": pkg.FirmwareVersion, "hub_ota_v1": true})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	p, err := newPico(server.URL, strings.Repeat("a", 64), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"", "transferring", "selecting", "confirming"} {
		err := p.InstallFirmware(context.Background(), identity.UID, pkg, stage, nil)
		problem, ok := err.(*Error)
		if !ok || problem.Code != "firmware_already_installed" {
			t.Fatalf("stage %q: %v", stage, err)
		}
	}
}

// The component's standalone tests run without a compiler or firmware checkout.
// CI also runs this test against the actual recovery dispatcher and flash model.
func TestHubOTAWithNativeRecovery(t *testing.T) {
	for _, target := range []struct{ model, display string }{{"pico-s", ""}, {"pico-c", "ssd1309"}, {"pico-c", "ssd1325"}} {
		t.Run(target.model+target.display, func(t *testing.T) { testHubOTAWithNativeRecovery(t, target.model, target.display, false) })
	}
	t.Run("revised Pico S 1.0.0", func(t *testing.T) { testHubOTAWithNativeRecovery(t, "pico-s", "", true) })
}
func testHubOTAWithNativeRecovery(t *testing.T, model, display string, revision bool) {
	harness := os.Getenv("PICO_HUB_RECOVERY_TEST")
	if harness == "" {
		t.Skip("set PICO_HUB_RECOVERY_TEST to the recovery HTTP host fixture")
	}
	python := os.Getenv("PYTHON")
	if python == "" {
		python = "python3"
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, python, harness, display)
	command.Stderr = os.Stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); command.Wait() }()
	var port int
	if _, err = fmt.Fscan(bufio.NewReader(stdout), &port); err != nil {
		t.Fatal(err)
	}
	endpoint, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	proxy := httputil.NewSingleHostReverseProxy(endpoint)
	uid := "0123456789abcdef0123456789abcdef"
	image := make([]byte, 2700)
	binary.LittleEndian.PutUint32(image, 0x2003f000)
	binary.LittleEndian.PutUint32(image[4:], 0x20004009)
	if display != "" {
		copy(image[1024:], []byte("PicoBrew:pico-c:"+display+"\x00"))
	}
	pkg := firmware.UpdatePayload{Candidate: firmware.Candidate{Model: model, DisplayVariant: display, FirmwareVersion: "1.3.0", BinaryBytes: len(image), BinarySHA256: firmware.Digest(image)}}
	pkg.Files[0] = image
	for i := 1; i < len(pkg.Files); i++ {
		pkg.Files[i] = append([]byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 0, 3}, bytes.Repeat([]byte{byte(i)}, 1400)...)
	}
	old := otaIdentity{UID: uid, Model: pkg.Model, DisplayVariant: display, Digest: strings.Repeat("f", 64)}
	installedVersion := "1.2.0"
	if revision {
		archive, err := os.ReadFile(filepath.Join(filepath.Dir(harness), "../releases/pico-s/1.0.0.zip"))
		if err != nil {
			t.Fatal(err)
		}
		pkg, err = firmware.ReadUpdatePayload(archive)
		if err != nil {
			t.Fatal(err)
		}
		pkg.ReplacesPackageID = "f0d4798eb31ce28d7df870b0e29e592e0362c1ac93da307f550a0acf8856bf6d"
		installedVersion = pkg.FirmwareVersion
		image = pkg.Files[0]
	}
	target := otaIdentity{UID: uid, Model: pkg.Model, DisplayVariant: display, Digest: pkg.BinarySHA256}
	key := strings.Repeat("a", 64)
	var mu sync.Mutex
	state := "normal"
	interrupt := true
	entered, confirmed, imageUploads := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(401)
			return
		}
		if state == "recovery" {
			if !strings.HasPrefix(r.URL.Path, "/api/v1/recovery") {
				w.WriteHeader(404)
				return
			}
			reply := httptest.NewRecorder()
			proxy.ServeHTTP(reply, r)
			if r.URL.Path == "/api/v1/recovery/finish" && reply.Code == 200 {
				var ack recoveryAck
				json.Unmarshal(reply.Body.Bytes(), &ack)
				if ack.Mask == 1 {
					imageUploads++
				}
				if interrupt && ack.Mask == 1 {
					interrupt = false
					w.WriteHeader(503)
					return
				}
			}
			if r.URL.Path == "/api/v1/recovery/select" && reply.Code == 200 {
				state = "trial"
			}
			w.WriteHeader(reply.Code)
			w.Write(reply.Body.Bytes())
			return
		}
		value := old
		if state != "normal" {
			value = target
		}
		switch r.URL.Path {
		case "/api/v1/ota":
			json.NewEncoder(w).Encode(otaState{otaIdentity: value, Size: len(image), OK: true, Trial: state == "trial"})
		case "/api/v1/ota/enter", "/api/v1/ota/confirm":
			var claimed otaIdentity
			if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&claimed) != nil || value != claimed {
				w.WriteHeader(409)
				return
			}
			if r.URL.Path == "/api/v1/ota/enter" && state == "normal" {
				state = "recovery"
				entered++
			} else if r.URL.Path == "/api/v1/ota/confirm" && state == "trial" {
				state = "confirmed"
				confirmed++
			} else {
				w.WriteHeader(409)
				return
			}
			json.NewEncoder(w).Encode(otaState{otaIdentity: value, Size: len(image), OK: true})
		case "/api/v1/status":
			json.NewEncoder(w).Encode(map[string]any{"device_uid": uid, "model": pkg.Model, "display_variant": display, "ready": true})
		case "/api/v1":
			version := pkg.FirmwareVersion
			if state == "normal" {
				version = installedVersion
			}
			json.NewEncoder(w).Encode(map[string]any{"model": pkg.Model, "display_variant": display, "firmware_version": version, "hub_ota_v1": true})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	p, err := newPico(server.URL, key, true)
	if err != nil {
		t.Fatal(err)
	}
	lastStage := ""
	checkpoint := func(stage string) error { lastStage = stage; return nil }
	if err = p.InstallFirmware(ctx, "other-device", pkg, "", checkpoint); err == nil {
		t.Fatal("changed device identity accepted")
	}
	if revision {
		if err = p.InstallFirmware(ctx, uid, pkg, "", checkpoint); err == nil || lastStage != "" {
			t.Fatal("unrecognized live source entered the migration")
		}
		mu.Lock()
		old.Digest = "5b221f2f52afceab4c9fe7d1c391071b708f439d9e1be8cf883addef584852af"
		mu.Unlock()
		other := pkg
		other.ArchiveSHA256 = strings.Repeat("0", 64)
		if err = p.InstallFirmware(ctx, uid, other, "", checkpoint); err == nil || lastStage != "" {
			t.Fatal("different archive accepted for migration")
		}
	}
	if err = p.InstallFirmware(ctx, uid, pkg, "", checkpoint); err == nil {
		t.Fatal("lost acknowledgement reported success")
	}
	if display != "" {
		other := "ssd1325"
		if display == other {
			other = "ssd1309"
		}
		if _, ready := p.readRecovery(ctx, uid, other, 2, len(image)); ready {
			t.Fatal("recovery accepted different controller after a lost acknowledgement")
		}
		wrong := pkg
		wrong.DisplayVariant = other
		if err := p.transferFirmware(ctx, wrong, checkpoint); err == nil {
			t.Fatal("recovery accepted cross-display plan")
		}
		wrong = pkg
		wrong.Files[0] = append([]byte(nil), image...)
		copy(wrong.Files[0][1024:], []byte("PicoBrew:pico-c:"+other+"\x00"))
		wrong.BinarySHA256 = firmware.Digest(wrong.Files[0])
		if err := p.transferFirmware(ctx, wrong, checkpoint); err == nil {
			t.Fatal("recovery selected image for a different display")
		}
	}
	resumeStage := "transferring"
	if revision {
		if lastStage != "entering" {
			t.Fatalf("lost source verification checkpoint: %q", lastStage)
		}
		resumeStage = lastStage
	}
	if err = p.InstallFirmware(ctx, uid, pkg, resumeStage, checkpoint); err != nil {
		t.Fatal(err)
	}
	if err = p.InstallFirmware(ctx, uid, pkg, "confirming", checkpoint); err != nil {
		t.Fatal(err)
	}
	if revision && p.InstallFirmware(ctx, uid, pkg, "", checkpoint) == nil {
		t.Fatal("confirmed revised application accepted a fresh same-version installation")
	}
	mu.Lock()
	defer mu.Unlock()
	expectedWrites := 1
	if display != "" {
		expectedWrites = 3
	}
	if state != "confirmed" || entered != 1 || confirmed != 1 || imageUploads != expectedWrites {
		t.Fatalf("state=%s entries=%d confirms=%d image writes=%d", state, entered, confirmed, imageUploads)
	}
}
