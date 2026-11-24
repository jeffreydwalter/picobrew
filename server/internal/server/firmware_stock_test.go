package server

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/firmware"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/firmware/firmwaretest"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/stockfirmware"
)

type stableObserver struct {
	*pairingAdapter
	stableErr error
}

func (a *stableObserver) VerifyStableApplication(_ context.Context, uid, model string) error {
	if uid != a.status.DeviceUID || model != a.status.Model {
		return errors.New("wrong identity")
	}
	return a.stableErr
}

func TestStockRestoreUpgradeHintOnlyForEarlierHubOTAReleases(t *testing.T) {
	for _, v := range []string{"1.3.0", "1.3.1", "1.3.2", "1.3.3", "1.3.4", "1.3.5"} {
		if !stockUpgradeAvailable(v) {
			t.Fatalf("missing update hint for %q", v)
		}
	}
	for _, v := range []string{"", "1.2.0", "1.3.6", "1.3.7", "1.3.10", "2.0.0", "preview"} {
		if stockUpgradeAvailable(v) {
			t.Fatalf("unwarranted update hint for %q", v)
		}
	}
}

func TestStockObservationRequiresACompletedImageReadback(t *testing.T) {
	for _, stage := range []string{"", "preparing", "staging_worker", "selecting_worker", "application", "recovery"} {
		for _, state := range []string{"running", "interrupted", "restart_requested"} {
			if canObserveStock(&firmware.StockReceipt{Stage: stage, State: state}) {
				t.Fatalf("early observation accepted at %s/%s", stage, state)
			}
		}
	}
	if canObserveStock(&firmware.StockReceipt{Stage: "committing", State: "restart_requested"}) {
		t.Fatal("inconsistent receipt accepted")
	}
}

func TestOwnerCanConfirmOnlyTheirVerifiedStockHandoffWhileOffline(t *testing.T) {
	s, d, _ := identityFixture(t, filepath.Join(t.TempDir(), "store.sqlite"), "unit")
	uid := "unit"
	if _, err := s.db.Put("device-identity", d.view.ID, deviceIdentity{UID: uid}, 0); err != nil {
		t.Fatal(err)
	}
	d.view.Online = false
	d.awaitingIdentity = true
	d.view.ModelSource = "firmware"
	receipt := firmware.StockReceipt{UID: uid, Model: "pico-pro", Started: time.Unix(0, 0).UTC(), Stage: "recovery", State: "interrupted"}
	version, err := s.db.Put("firmware-stock-restore", d.view.ID, receipt, 0)
	if err != nil {
		t.Fatal(err)
	}
	observe := func(uid, started string) int {
		w := httptest.NewRecorder()
		s.observeStockStartup(w, identityRequest("POST", "/api/v1/devices/registered/firmware/stock-started", `{"device_uid":"`+uid+`","started":"`+started+`"}`))
		return w.Code
	}
	timeString := receipt.Started.Format(time.RFC3339)
	if observe(uid, timeString) != 409 {
		t.Fatal("unverified boot handoff accepted")
	}
	// A lost /commit reply can coincide with an actual reboot to OEM. Only
	// owner observation can close this case. The selector remains unverified.
	receipt.Stage = "committing"
	version, err = s.db.Put("firmware-stock-restore", d.view.ID, receipt, version)
	if err != nil {
		t.Fatal(err)
	}
	if observe("other", timeString) != 409 || observe(uid, "2000-01-01T00:00:00Z") != 409 {
		t.Fatal("wrong interrupted stock receipt accepted")
	}
	w := httptest.NewRecorder()
	s.machineFirmware(w, identityRequest("GET", "/api/v1/devices/registered/firmware", ""))
	if !bytes.Contains(w.Body.Bytes(), []byte(`"can_confirm_stock_startup":true`)) {
		t.Fatal("offline lost commit cannot be closed")
	}
	if observe(uid, timeString) != 200 {
		t.Fatal("lost commit owner observation rejected")
	}
	reported, version, err := s.stockReceipt(d.view.ID)
	if err != nil || stockPending(reported) || reported.State != "owner_reported" || reported.Stage != "committing" ||
		!strings.Contains(reported.Message, "did not confirm the boot selection") || !strings.Contains(reported.Message, "reported seeing") || observe(uid, timeString) != 409 {
		t.Fatal("owner observation incorrectly treated as verified boot selection", reported, err)
	}
	receipt.Stage = "rebooting"
	if _, err = s.db.Put("firmware-stock-restore", d.view.ID, receipt, version); err != nil {
		t.Fatal(err)
	}
	if observe("another", timeString) != 409 || observe(uid, "2000-01-01T00:00:00Z") != 409 {
		t.Fatal("mismatched observer accepted")
	}
	d.identityMismatch = true
	d.awaitingIdentity = false
	if observe(uid, timeString) != 409 {
		t.Fatal("address mismatch accepted")
	}
	d.awaitingIdentity = true
	w = httptest.NewRecorder()
	s.machineFirmware(w, identityRequest("GET", "/api/v1/devices/registered/firmware", ""))
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"can_confirm_stock_startup":true`)) {
		t.Fatal("verified offline handoff missing", w.Body.String())
	}
	if got := observe(uid, timeString); got != 200 {
		t.Fatal(got)
	}
	final, _, err := s.stockReceipt(d.view.ID)
	if err != nil || stockPending(final) || final.State != "owner_confirmed" || !strings.Contains(final.Message, "You confirmed") {
		t.Fatal("expected human confirmation only", err, final)
	}
	if observe(uid, timeString) != 409 {
		t.Fatal("receipt reused")
	}
}

func TestPendingStockRestoreReleasedOnlyByFreshStableCustomBoot(t *testing.T) {
	s, d, _ := identityFixture(t, filepath.Join(t.TempDir(), "store.sqlite"), "unit")
	d.view.ModelSource = "firmware"
	a := &stableObserver{pairingAdapter: &pairingAdapter{status: domain.Status{DeviceUID: "unit", Model: "pico-pro", ModelFamily: "pico-s-pro", Ready: true}, caps: domain.Capabilities{Model: "pico-pro", ModelFamily: "pico-s-pro", HubOTAV1: true}}}
	d.adapter = a
	if _, err := s.verifiedStatus(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"preparing", "rebooting"} {
		receipt, previous, err := s.stockReceipt(d.view.ID)
		if err != nil {
			t.Fatal(err)
		}
		receipt = &firmware.StockReceipt{UID: "unit", Model: "pico-pro", Stage: stage, State: "interrupted"}
		v, err := s.db.Put("firmware-stock-restore", d.view.ID, *receipt, previous)
		if err != nil {
			t.Fatal(err)
		}
		a.stableErr = errors.New("unreachable or trial")
		if err = s.reconcileCustomBoot(context.Background(), d, "unit", receipt, v); err == nil {
			t.Fatal("unknown boot cleared journal")
		}
		a.stableErr = nil
		a.status.Busy = true
		if err = s.reconcileCustomBoot(context.Background(), d, "unit", receipt, v); err == nil {
			t.Fatal("busy brewer accepted")
		}
		a.status.Busy = false
		if err = s.reconcileCustomBoot(context.Background(), d, "other", receipt, v); err == nil {
			t.Fatal("different UID accepted")
		}
		if err = s.reconcileCustomBoot(context.Background(), d, "unit", receipt, v); err != nil {
			t.Fatal(err)
		}
		got, _, _ := s.stockReceipt(d.view.ID)
		if got.State != "custom_verified" || stockPending(got) {
			t.Fatal("stable custom boot remained blocked", got)
		}
	}
}

type stockAdapterStub struct{ *resumeFirmwareAdapter }

func (a *stockAdapterStub) RestoreStock(context.Context, string, string, stockfirmware.RestoreImages, string, func(string) error) error {
	return errors.New("unexpected restore dispatch")
}

func TestStockRestoreHTTPRequiresConsentAndCompetingOperationsFailClosed(t *testing.T) {
	s, h, c, stop := startFirmwareServer(t, newTestDataDir(t))
	defer stop()
	csrf := signIn(t, c, h.URL)
	path := h.URL + "/api/v1/devices/brewer/firmware/restore-stock"
	for _, body := range []map[string]any{{"device_uid": "unit", "acknowledge_loss_of_hub": true}, {"device_uid": "unit", "confirm_restore": true}, {"confirm_restore": true, "acknowledge_loss_of_hub": true}} {
		status, content := request(t, c, "POST", path, csrf, body, nil)
		if status != 400 || !bytes.Contains(content, []byte("invalid_restore")) {
			t.Fatal(status, string(content))
		}
	}
	a := &resumeFirmwareAdapter{pairingAdapter: &pairingAdapter{status: domain.Status{Model: "pico-s", ModelFamily: "pico-s-pro", DeviceUID: "unit", Ready: true}, caps: domain.Capabilities{Model: "pico-s", ModelFamily: "pico-s-pro", HubOTAV1: true, FirmwareVersion: "1.0.0"}}, stages: make(chan string, 1)}
	d := s.attach(domain.Device{ID: "brewer", Model: "pico-s", ModelSource: "firmware", Adapter: "pico-standalone", Enabled: true}, nil)
	d.adapter = &stockAdapterStub{a}
	if _, err := s.verifiedStatus(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"device_uid": "unit", "acknowledge_loss_of_hub": true, "confirm_restore": true}
	if code, reply := request(t, c, "POST", path, csrf, body, nil); code != 409 || !bytes.Contains(reply, []byte("stock_restore_unavailable")) {
		t.Fatal("stock route accepted unqualified hardware", code, string(reply))
	}
	body["device_uid"] = "other"
	if code, reply := request(t, c, "POST", path, csrf, body, nil); code != 409 || !bytes.Contains(reply, []byte("identity_mismatch")) {
		t.Fatal("wrong unit accepted", code, string(reply))
	}
	if prior, _, err := s.stockReceipt(d.view.ID); prior != nil || err != nil {
		t.Fatal("stock restore persisted before passing preflight")
	}
	receipt := firmware.StockReceipt{UID: "unit", Model: "pico-s", Stage: "recovery", State: "interrupted"}
	if _, err := s.db.Put("firmware-stock-restore", "brewer", receipt, 0); err != nil {
		t.Fatal(err)
	}
	files := firmwaretest.Files("pico-s")
	firmwaretest.ChangeReport(files, func(r map[string]any) { r["hub_ota_protocol"] = 1 })
	code, archiveReply := uploadFirmware(t, c, h.URL, csrf, firmwaretest.Archive(files), nil)
	if code != 201 {
		t.Fatal(code, string(archiveReply))
	}
	pkg := parseFirmwarePackage(t, archiveReply)
	status, content := request(t, c, "POST", h.URL+"/api/v1/devices/brewer/firmware/install", csrf, map[string]any{"device_uid": "unit", "confirm": true, "package_id": pkg.ID}, nil)
	if status != 409 || !bytes.Contains(content, []byte("stock_restore_pending")) {
		t.Fatal(status, string(content))
	}
}
