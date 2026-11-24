package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/firmware"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/firmware/firmwaretest"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

func startFirmwareServer(t *testing.T, dir string) (*Server, *httptest.Server, *http.Client, func()) {
	t.Helper()
	s, err := New(Config{DataDir: dir, Password: ownerPassword, PollInterval: time.Hour,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	h := httptest.NewServer(s)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			h.Close()
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(stop)
	return s, h, client, stop
}

func firmwareUploadBody(archive []byte, field, extra string) ([]byte, string) {
	var data bytes.Buffer
	w := multipart.NewWriter(&data)
	p, _ := w.CreateFormFile(field, "package.zip")
	_, _ = p.Write(archive)
	if extra != "" {
		_ = w.WriteField(extra, "unexpected")
	}
	_ = w.Close()
	return data.Bytes(), w.FormDataContentType()
}

func uploadFirmware(t *testing.T, client *http.Client, url, csrf string, archive []byte, extraHeaders map[string]string) (int, []byte) {
	t.Helper()
	body, contentType := firmwareUploadBody(archive, "file", "")
	r, err := http.NewRequest(http.MethodPost, url+"/api/v1/firmware/packages", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", contentType)
	r.Header.Set("X-CSRF-Token", csrf)
	for key, value := range extraHeaders {
		r.Header.Set(key, value)
	}
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, data
}

func parseFirmwarePackage(t *testing.T, data []byte) firmware.Package {
	t.Helper()
	var p firmware.Package
	if err := json.Unmarshal(data, &p); err != nil || p.ID == "" {
		t.Fatalf("invalid package response: %s (%v)", data, err)
	}
	return p
}

func TestFirmwareUploadAuthPersistenceDownloadAndDuplicateProvenance(t *testing.T) {
	dir := newTestDataDir(t)
	s, h, client, stop := startFirmwareServer(t, dir)
	archive := firmwaretest.Archive(firmwaretest.Files("pico-pro"))
	if code, _ := uploadFirmware(t, client, h.URL, "", archive, nil); code != 401 {
		t.Fatalf("unauthenticated upload: %d", code)
	}
	csrf := signIn(t, client, h.URL)
	if code, _ := uploadFirmware(t, client, h.URL, "", archive, nil); code != 403 {
		t.Fatalf("missing upload CSRF: %d", code)
	}
	if code, _ := uploadFirmware(t, client, h.URL, csrf, archive, map[string]string{"Origin": "https://foreign.example"}); code != 403 {
		t.Fatalf("cross-origin upload: %d", code)
	}
	code, data := request(t, client, "GET", h.URL+"/api/v1/firmware/settings", "", nil, nil)
	if code != 200 || !bytes.Contains(data, []byte(firmware.DefaultRepository)) || !bytes.Contains(data, []byte(`"ref":""`)) {
		t.Fatalf("default settings: %d %s", code, data)
	}
	code, data = request(t, client, "PUT", h.URL+"/api/v1/firmware/settings", csrf, firmware.Settings{Repository: "https://github.com/Owner/Repo", Ref: "stable"}, nil)
	if code != 200 || !bytes.Contains(data, []byte(`"repository":"owner/repo"`)) {
		t.Fatalf("settings normalization: %d %s", code, data)
	}
	code, data = uploadFirmware(t, client, h.URL, csrf, archive, nil)
	if code != 201 {
		t.Fatalf("upload: %d %s", code, data)
	}
	p := parseFirmwarePackage(t, data)
	if p.Provenance.Source != "upload" || p.Provenance.Repository != "" || p.Provenance.Commit != "" ||
		p.Validation != "passed" || p.ID != firmware.Digest(archive) || p.PackageBytes != len(archive) {
		t.Fatalf("unsafe upload metadata: %+v", p)
	}
	code, data = uploadFirmware(t, client, h.URL, csrf, archive, nil)
	if duplicate := parseFirmwarePackage(t, data); code != 200 || duplicate != p {
		t.Fatalf("duplicate changed immutable metadata: %d %s", code, data)
	}
	code, data = request(t, client, "GET", h.URL+"/api/v1/firmware/packages/"+p.ID+"/download", "", nil, nil)
	if code != 200 || !bytes.Equal(data, archive) {
		t.Fatal("download does not preserve validated archive", code)
	}
	if records, err := s.db.List(firmwareRecordKind); err != nil || len(records) != 1 {
		t.Fatalf("unexpected persisted records: %d, %v", len(records), err)
	}
	stop()
	_, h2, c2, _ := startFirmwareServer(t, dir)
	csrf2 := signIn(t, c2, h2.URL)
	code, data = request(t, c2, "GET", h2.URL+"/api/v1/firmware/settings", "", nil, nil)
	if code != 200 || !bytes.Contains(data, []byte(`"ref":"stable"`)) || !bytes.Contains(data, []byte(`"repository":"owner/repo"`)) {
		t.Fatalf("settings lost on reopen: %d %s", code, data)
	}
	code, data = request(t, c2, "GET", h2.URL+"/api/v1/firmware/packages/"+p.ID, "", nil, nil)
	if reopened := parseFirmwarePackage(t, data); code != 200 || reopened != p {
		t.Fatalf("package lost on reopen: %d %s", code, data)
	}
	code, data = request(t, c2, "GET", h2.URL+"/api/v1/firmware/packages/"+p.ID+"/download", "", nil, nil)
	if code != 200 || !bytes.Equal(data, archive) {
		t.Fatal("archive lost on reopen", code)
	}
	if err := os.WriteFile(filepath.Join(dir, "firmware", p.ID+".zip"), []byte("damaged bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	code, data = request(t, c2, "GET", h2.URL+"/api/v1/firmware/packages/"+p.ID+"/download", "", nil, nil)
	if code != 409 || !bytes.Contains(data, []byte("firmware_cache_corrupt")) {
		t.Fatalf("damaged cache served successfully: %d %s", code, data)
	}
	code, data = request(t, c2, "DELETE", h2.URL+"/api/v1/firmware/packages/"+p.ID, csrf2, nil, nil)
	if code != 200 || !bytes.Contains(data, []byte(`"deleted":true`)) {
		t.Fatalf("damaged package removal: %d %s", code, data)
	}
	if code, data = uploadFirmware(t, c2, h2.URL, csrf2, archive, nil); code != 201 {
		t.Fatalf("reimport after removal: %d %s", code, data)
	}
}

func TestFirmwareUploadRejectsInvalidAndOversizedBodiesWithoutPublication(t *testing.T) {
	s, h, client := newTestServer(t)
	csrf := signIn(t, client, h.URL)
	for name, archive := range map[string][]byte{
		"bad zip":   []byte("not a zip"),
		"too large": make([]byte, firmware.MaxArchiveBytes+1),
	} {
		code, _ := uploadFirmware(t, client, h.URL, csrf, archive, nil)
		expected := 422
		if name == "too large" {
			expected = 413
		}
		if code != expected {
			t.Errorf("%s: %d", name, code)
		}
	}
	for _, fields := range [][2]string{{"wrong", ""}, {"file", "extra"}} {
		body, ct := firmwareUploadBody(firmwaretest.Archive(firmwaretest.Files("pico-s")), fields[0], fields[1])
		r, _ := http.NewRequest("POST", h.URL+"/api/v1/firmware/packages", bytes.NewReader(body))
		r.Header.Set("Content-Type", ct)
		r.Header.Set("X-CSRF-Token", csrf)
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatalf("unexpected multipart fields accepted: %d", resp.StatusCode)
		}
	}
	code, _ := request(t, client, "POST", h.URL+"/api/v1/firmware/packages", csrf, map[string]any{}, nil)
	if code != 415 {
		t.Fatalf("JSON upload accepted: %d", code)
	}
	s.firmwareWorkMu.Lock()
	code, _ = uploadFirmware(t, client, h.URL, csrf, firmwaretest.Archive(firmwaretest.Files("pico-s")), nil)
	s.firmwareWorkMu.Unlock()
	if code != 429 {
		t.Fatalf("concurrent unbounded upload accepted: %d", code)
	}
	rows, err := s.db.List(firmwareRecordKind)
	if err != nil || len(rows) != 0 {
		t.Fatal("failed upload published metadata", err)
	}
}

type firmwareTransport func(*http.Request) (*http.Response, error)

func (fn firmwareTransport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func fixtureGitHubResponse(r *http.Request, files map[string][]byte) *http.Response {
	for name, data := range files {
		if strings.HasSuffix(r.URL.Path, "/"+name) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}
		}
	}
	return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}
}

func TestFirmwareGitHubSourceChangesAreRejectedBeforeAndDuringImport(t *testing.T) {
	s, h, client := newTestServer(t)
	csrf := signIn(t, client, h.URL)
	const commit = "0123456789abcdef0123456789abcdef01234567"
	selection := map[string]string{"repository": firmware.DefaultRepository, "commit": commit, "model": "pico-pro"}
	var requests atomic.Int32
	s.firmwareGitHub.HTTP.Transport = firmwareTransport(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		return fixtureGitHubResponse(r, firmwaretest.Files("pico-pro")), nil
	})
	code, data := request(t, client, "POST", h.URL+"/api/v1/firmware/github/import", csrf,
		map[string]string{"repository": "other/source", "commit": commit, "model": "pico-pro"}, nil)
	if code != 409 || !bytes.Contains(data, []byte("firmware_source_changed")) || requests.Load() != 0 {
		t.Fatalf("changed repository reached network: %d %s", code, data)
	}
	for _, change := range []firmware.Settings{
		{Repository: firmware.DefaultRepository, Ref: "new-ref"},
		{Repository: "other/source"},
	} {
		_, _ = request(t, client, "PUT", h.URL+"/api/v1/firmware/settings", csrf, firmware.Settings{Repository: firmware.DefaultRepository}, nil)
		started, release := make(chan struct{}), make(chan struct{})
		var first sync.Once
		files := firmwaretest.Files("pico-pro")
		s.firmwareGitHub.HTTP.Transport = firmwareTransport(func(r *http.Request) (*http.Response, error) {
			first.Do(func() {
				close(started)
				select {
				case <-release:
				case <-r.Context().Done():
				}
			})
			return fixtureGitHubResponse(r, files), nil
		})
		payload, _ := json.Marshal(selection)
		req, _ := http.NewRequest("POST", h.URL+"/api/v1/firmware/github/import", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		type result struct {
			status int
			body   []byte
			err    error
		}
		finished := make(chan result, 1)
		go func() {
			resp, err := client.Do(req)
			if err != nil {
				finished <- result{err: err}
				return
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			finished <- result{status: resp.StatusCode, body: body, err: err}
		}()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("import did not start")
		}
		code, data = request(t, client, "PUT", h.URL+"/api/v1/firmware/settings", csrf, change, nil)
		close(release)
		if code != 200 {
			t.Fatalf("source edit failed: %d %s", code, data)
		}
		got := <-finished
		if got.err != nil || got.status != 409 || !bytes.Contains(got.body, []byte("firmware_source_changed")) {
			t.Fatalf("stale source published: %+v %s", got, got.body)
		}
	}
	rows, err := s.db.List(firmwareRecordKind)
	if err != nil || len(rows) != 0 {
		t.Fatal("source conflict published package metadata")
	}
}

func TestFirmwareGitHubImportProvenanceAndNetworkFailure(t *testing.T) {
	s, h, client := newTestServer(t)
	csrf := signIn(t, client, h.URL)
	const commit = "0123456789abcdef0123456789abcdef01234567"
	selection := map[string]string{"repository": firmware.DefaultRepository, "commit": commit, "model": "pico-pro"}
	files := firmwaretest.Files("pico-pro")
	s.firmwareGitHub.HTTP.Transport = firmwareTransport(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/"+commit+"/") || r.URL.Host != "raw.githubusercontent.com" {
			t.Errorf("import did not use pinned source: %s", r.URL)
		}
		return fixtureGitHubResponse(r, files), nil
	})
	code, data := request(t, client, "POST", h.URL+"/api/v1/firmware/github/import", csrf, selection, nil)
	if code != 201 {
		t.Fatalf("GitHub import: %d %s", code, data)
	}
	p := parseFirmwarePackage(t, data)
	if p.Provenance.Source != "github" || p.Provenance.Repository != firmware.DefaultRepository || p.Provenance.Commit != commit {
		t.Fatalf("wrong provenance: %+v", p)
	}
	code, archive := request(t, client, "GET", h.URL+"/api/v1/firmware/packages/"+p.ID+"/download", "", nil, nil)
	if code != 200 {
		t.Fatal(code)
	}
	code, data = uploadFirmware(t, client, h.URL, csrf, archive, nil)
	if duplicate := parseFirmwarePackage(t, data); code != 200 || duplicate != p {
		t.Fatalf("local duplicate rewrote GitHub provenance: %d %s", code, data)
	}
	s.firmwareGitHub.HTTP.Transport = firmwareTransport(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("a private transport diagnostic")
	})
	code, data = request(t, client, "POST", h.URL+"/api/v1/firmware/github/import", csrf, selection, nil)
	if code != 502 || bytes.Contains(data, []byte("private transport")) {
		t.Fatalf("unsafe network failure: %d %s", code, data)
	}
	rows, _ := s.db.List(firmwareRecordKind)
	if len(rows) != 1 {
		t.Fatal("failed import published another package")
	}
}

func TestFirmwareCacheRecoversInterruptedPublicationsUnderExclusiveLock(t *testing.T) {
	s, h, client := newTestServer(t)
	csrf := signIn(t, client, h.URL)
	archive := firmwaretest.Archive(firmwaretest.Files("pico-s"))
	lock, err := s.lockFirmwareCache()
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(s.firmwareCacheDir(), ".incoming-"+strings.Repeat("a", 32))
	orphan := filepath.Join(s.firmwareCacheDir(), firmware.Digest(archive)+".zip")
	unknown := filepath.Join(s.firmwareCacheDir(), "leave-this-file.txt")
	for _, path := range []string{stage, orphan, unknown} {
		if err := os.WriteFile(path, []byte("interrupted write"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	code, data := uploadFirmware(t, client, h.URL, csrf, archive, nil)
	if code != 429 {
		t.Fatalf("active cache lock ignored: %d %s", code, data)
	}
	if _, err := os.Stat(stage); err != nil {
		t.Fatal("a live staging file was removed", err)
	}
	_ = lock.Close()
	code, data = uploadFirmware(t, client, h.URL, csrf, archive, nil)
	if code != 201 {
		t.Fatalf("orphan recovery failed: %d %s", code, data)
	}
	if _, err := os.Stat(stage); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("abandoned staging file remains", err)
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatal("recovery removed an unrecognized file", err)
	}
	actual, err := os.ReadFile(orphan)
	if err != nil || !bytes.Equal(actual, archive) {
		t.Fatal("corrupted orphan was not safely replaced", err)
	}
}

func TestFirmwareCacheQuotaAndDeletion(t *testing.T) {
	_, h, client := newTestServer(t)
	csrf := signIn(t, client, h.URL)
	var first firmware.Package
	for i := 0; i <= firmware.MaxPackages; i++ {
		files := firmwaretest.Files("pico-pro")
		firmwaretest.ChangeReport(files, func(r map[string]any) { r["firmware_version"] = "1.2.0-" + strings.Repeat("x", i) })
		code, data := uploadFirmware(t, client, h.URL, csrf, firmwaretest.Archive(files), nil)
		if i < firmware.MaxPackages {
			if code != 201 {
				t.Fatalf("package %d: %d %s", i, code, data)
			}
			if i == 0 {
				first = parseFirmwarePackage(t, data)
			}
		} else if code != 409 || !bytes.Contains(data, []byte("firmware_cache_full")) {
			t.Fatalf("cache quota not enforced: %d %s", code, data)
		}
	}
	if code, _ := request(t, client, "DELETE", h.URL+"/api/v1/firmware/packages/"+first.ID, "", nil, nil); code != 403 {
		t.Fatalf("deletion without CSRF: %d", code)
	}
	code, data := request(t, client, "DELETE", h.URL+"/api/v1/firmware/packages/"+first.ID, csrf, nil, nil)
	if code != 200 {
		t.Fatalf("delete: %d %s", code, data)
	}
	code, data = uploadFirmware(t, client, h.URL, csrf, firmwaretest.Archive(firmwaretest.Files("pico-s")), nil)
	if code != 201 {
		t.Fatalf("deletion did not free capacity: %d %s", code, data)
	}
}

func TestMachineFirmwareIsReadOnlyAndAvailableForUnreadyMachines(t *testing.T) {
	s, h, client := newTestServer(t)
	csrf := signIn(t, client, h.URL)
	for _, model := range []string{"pico-s", "pico-pro"} {
		files := firmwaretest.Files(model)
		for _, protocol := range []int{0, 1} {
			firmwaretest.ChangeReport(files, func(report map[string]any) { report["hub_ota_protocol"] = protocol })
			if code, data := uploadFirmware(t, client, h.URL, csrf, firmwaretest.Archive(files), nil); code != 201 {
				t.Fatalf("setup package: %d %s", code, data)
			}
		}
	}
	for _, test := range []struct {
		id       string
		adapter  string
		enabled  bool
		reported string
		source   string
		mismatch bool
		want     int
	}{
		{"verified", "pico-standalone", true, "pico-pro", "firmware", false, 1},
		{"disabled", "pico-standalone", false, "pico-s", "firmware", false, 1},
		{"unknown", "pico-standalone", true, "", "pending", false, 0},
		{"missing-key", "pico-standalone", false, "", "pending", false, 0},
		{"legacy-manual", "pico-standalone", true, "", "manual", false, 0},
		{"wrong-uid", "pico-standalone", true, "pico-pro", "firmware", true, 0},
		{"unsupported", "pico-standalone", true, "z", "firmware", false, 0},
		{"simulator", "simulator", true, "pico-pro", "firmware", false, 0},
	} {
		d := &runtimeDevice{cancel: func() {}, identityMismatch: test.mismatch,
			view: domain.DeviceView{Device: domain.Device{ID: test.id, Adapter: test.adapter, Enabled: test.enabled, Model: "pro", ModelSource: test.source}}}
		if test.reported != "" {
			d.view.Status = &domain.Status{DeviceUID: "accepted-uid", Model: test.reported}
			d.view.Capabilities = &domain.Capabilities{Model: test.reported, ModelFamily: "pico-s-pro", FirmwareVersion: "1.2.0"}
		}
		// adapter deliberately nil: a read-only readiness view must not invoke
		// any network/device method or require a configured control adapter.
		s.mu.Lock()
		s.devices[test.id] = d
		s.mu.Unlock()
		code, data := request(t, client, "GET", h.URL+"/api/v1/devices/"+test.id+"/firmware", "", nil, nil)
		var got firmware.MachineFirmware
		if err := json.Unmarshal(data, &got); err != nil || code != 200 || got.InstallSupported || len(got.Packages) != test.want || got.Reason == "" {
			t.Fatalf("%s readiness: %d %s (%v)", test.id, code, data, err)
		}
		if test.want == 0 && got.Model != "" {
			t.Fatalf("%s incorrectly implies model matching: %+v", test.id, got)
		}
		for _, p := range got.Packages {
			if !p.SupportsHubOTA() || p.Model != test.reported {
				t.Fatalf("offered inapplicable cached package: %+v", p)
			}
		}
		if got.StockRestoreGuide {
			t.Fatal("advertised a stock restore for an unreviewed identity")
		}
	}
	code, data := request(t, client, "GET", h.URL+"/api/v1/firmware/packages", "", nil, nil)
	var archived []firmware.Package
	if err := json.Unmarshal(data, &archived); err != nil || code != 200 || len(archived) != 4 {
		t.Fatalf("archive inventory: %d %s (%v)", code, data, err)
	}
}

func TestStockRestoreGuideRequiresPinnedPhysicalMachine(t *testing.T) {
	s, d, _ := identityFixture(t, filepath.Join(t.TempDir(), "identity.sqlite"), "test-unit")
	d.identityMismatch = false
	d.view.ModelSource = "firmware"
	d.view.Capabilities = &domain.Capabilities{Model: "pico-s", ModelFamily: "pico-s-pro"}
	d.view.Status = &domain.Status{DeviceUID: "test-unit", Model: "pico-s"}
	read := func() firmware.MachineFirmware {
		t.Helper()
		w := httptest.NewRecorder()
		s.machineFirmware(w, identityRequest("GET", "/api/v1/devices/registered/firmware", ""))
		var got firmware.MachineFirmware
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 {
			t.Fatalf("firmware response: %d %s (%v)", w.Code, w.Body.Bytes(), err)
		}
		return got
	}
	if read().StockRestoreGuide {
		t.Fatal("missing identity pin allowed machine-specific restore guide")
	}
	if _, err := s.db.Put("device-identity", d.view.ID, deviceIdentity{UID: d.view.Status.DeviceUID}, 0); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"pico-s", "pico-pro", "z-series", "picoferm", "picostill"} {
		family := model
		if model == "pico-s" || model == "pico-pro" {
			family = "pico-s-pro"
		}
		d.view.Model = model
		d.view.Capabilities = &domain.Capabilities{Model: model, ModelFamily: family}
		d.view.Status = &domain.Status{DeviceUID: "test-unit", Model: model, ModelFamily: family}
		got := read()
		if !got.StockRestoreGuide || got.USBRestoreTarget == nil || got.USBRestoreTarget.UID != "test-unit" || got.USBRestoreTarget.Model != model || got.InstallSupported {
			t.Fatalf("%s USB eligibility: %+v", model, got)
		}
	}
	s.cfg.Demo = true
	if read().StockRestoreGuide {
		t.Fatal("demo offered restore guide")
	}
	s.cfg.Demo = false
	d.identityMismatch = true
	if read().StockRestoreGuide {
		t.Fatal("mismatched machine offered restore guide")
	}
	d.awaitingIdentity = true
	d.view.Online = false
	d.view.Capabilities, d.view.Status = nil, nil
	offline := read()
	if !offline.StockRestoreGuide || offline.USBRestoreTarget == nil || offline.USBRestoreTarget.Model != d.view.Model ||
		offline.USBRestoreTarget.UID != "test-unit" || offline.InstallSupported || offline.Model != "" {
		t.Fatalf("offline guidance must be independent of wireless eligibility: %+v", offline)
	}
}

func TestDemoFirmwareAcquisitionRemainsExplicitAndLocal(t *testing.T) {
	s, err := New(Config{DataDir: newTestDataDir(t), Demo: true, PollInterval: time.Hour, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var network atomic.Int32
	s.firmwareGitHub.HTTP.Transport = firmwareTransport(func(*http.Request) (*http.Response, error) {
		network.Add(1)
		return nil, context.Canceled
	})
	h := httptest.NewServer(s)
	defer h.Close()
	if code, data := request(t, h.Client(), "GET", h.URL+"/api/v1/firmware/settings", "", nil, nil); code != 200 {
		t.Fatalf("demo settings: %d %s", code, data)
	}
	if code, data := uploadFirmware(t, h.Client(), h.URL, "demo", firmwaretest.Archive(firmwaretest.Files("pico-s")), nil); code != 201 {
		t.Fatalf("explicit demo upload: %d %s", code, data)
	}
	if network.Load() != 0 {
		t.Fatal("demo settings/upload automatically contacted GitHub")
	}
	req := httptest.NewRequest("GET", "http://untrusted.example/api/v1/firmware/github", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 403 || network.Load() != 0 {
		t.Fatal("demo firmware route accepts nonlocal Host or contacted GitHub", rec.Code)
	}
}

func TestVersionCheckUsesMetadataAndStagesOnlySelectedArchive(t *testing.T) {
	s, h, c, close := startFirmwareServer(t, newTestDataDir(t))
	defer close()
	csrf := signIn(t, c, h.URL)
	parts := firmwaretest.Files("pico-s")
	firmwaretest.ChangeReport(parts, func(r map[string]any) { r["hub_ota_protocol"] = 1 })
	archive := firmwaretest.Archive(parts)
	candidate, err := firmware.ValidateArchive(archive)
	if err != nil {
		t.Fatal(err)
	}
	release := firmware.Release{Candidate: candidate, ArchiveSHA256: firmware.Digest(archive), SourceCommit: strings.Repeat("a", 40), Kind: "standalone"}
	index, _ := json.Marshal(map[string]any{"schema_version": 1, "releases": []firmware.Release{release}})
	files := map[string][]byte{"releases.json": index, firmware.DefaultRepository: []byte(`{"default_branch":"master"}`), "commits/master": []byte(`{"sha":"` + release.SourceCommit + `"}`), release.FirmwareVersion + ".zip": archive}
	reads := 0
	s.firmwareGitHub.HTTP = &http.Client{Transport: firmwareTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, ".zip") {
			reads++
		}
		return fixtureGitHubResponse(r, files), nil
	})}
	d := &runtimeDevice{cancel: func() {}, view: domain.DeviceView{Device: domain.Device{ID: "test", Name: "Brewer", Adapter: "pico-standalone", Model: "pico-s"}, Capabilities: &domain.Capabilities{Model: "pico-s", FirmwareVersion: "0.1.0"}}}
	s.mu.Lock()
	s.devices["test"] = d
	s.mu.Unlock()
	code, body := request(t, c, "POST", h.URL+"/api/v1/firmware/updates", csrf, nil, nil)
	var got releaseView
	if code != 200 || json.Unmarshal(body, &got) != nil || len(got.Releases) != 1 || len(got.Notices) != 1 || reads != 0 {
		t.Fatalf("version check %d %s; archive reads %d", code, body, reads)
	}
	for range 2 {
		code, body = request(t, c, "POST", h.URL+"/api/v1/firmware/releases/"+release.ArchiveSHA256+"/stage", csrf, nil, nil)
		if code != 200 || parseFirmwarePackage(t, body).ID != release.ArchiveSHA256 {
			t.Fatalf("stage %d %s", code, body)
		}
	}
	if reads != 1 {
		t.Fatalf("staging twice downloaded %d archives", reads)
	}

	var original firmware.Package
	recordVersion, err := s.db.Get(firmwareRecordKind, release.ArchiveSHA256, &original)
	if err != nil {
		t.Fatal(err)
	}
	cached := filepath.Join(s.firmwareCacheDir(), release.ArchiveSHA256+".zip")
	corrupt := []byte("damaged cached archive")
	if err := os.WriteFile(cached, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	stageURL := h.URL + "/api/v1/firmware/releases/" + release.ArchiveSHA256 + "/stage"
	files[release.FirmwareVersion+".zip"] = []byte("wrong upstream archive")
	code, body = request(t, c, "POST", stageURL, csrf, nil, nil)
	if code != 422 || !bytes.Contains(body, []byte("firmware_package_invalid")) {
		t.Fatalf("digest mismatch accepted as a repair: %d %s", code, body)
	}
	if actual, err := os.ReadFile(cached); err != nil || !bytes.Equal(actual, corrupt) {
		t.Fatal("invalid download replaced the cache", err)
	}
	files[release.FirmwareVersion+".zip"] = archive
	for _, missing := range []bool{false, true} {
		if missing {
			if err := os.Remove(cached); err != nil {
				t.Fatal(err)
			}
		}
		code, body = request(t, c, "POST", stageURL, csrf, nil, nil)
		if code != 200 || parseFirmwarePackage(t, body).ID != original.ID {
			t.Fatalf("repair of missing=%t: %d %s", missing, code, body)
		}
		if actual, err := os.ReadFile(cached); err != nil || !bytes.Equal(actual, archive) {
			t.Fatal("cache repair failed readback", err)
		}
		var after firmware.Package
		if version, err := s.db.Get(firmwareRecordKind, original.ID, &after); err != nil || version != recordVersion || after != original {
			t.Fatalf("repair changed original metadata or provenance: %+v %v", after, err)
		}
	}
}

type resumeFirmwareAdapter struct {
	*pairingAdapter
	stages chan string
}

func (a *resumeFirmwareAdapter) InstallFirmware(_ context.Context, _ string, _ firmware.UpdatePayload, stage string, _ func(string) error) error {
	a.stages <- stage
	return nil
}

func TestInstallUsesTheConnectedBrewersVersion(t *testing.T) {
	s, h, c, stop := startFirmwareServer(t, newTestDataDir(t))
	defer stop()
	csrf := signIn(t, c, h.URL)
	files := firmwaretest.Files("pico-s")
	firmwaretest.ChangeReport(files, func(report map[string]any) { report["hub_ota_protocol"] = 1 })
	code, body := uploadFirmware(t, c, h.URL, csrf, firmwaretest.Archive(files), nil)
	if code != 201 {
		t.Fatalf("upload: %d %s", code, body)
	}
	pkg := parseFirmwarePackage(t, body)
	registered := domain.Device{ID: "brewer", Model: "pico-s", ModelSource: "firmware", Enabled: true, Adapter: "pico-standalone"}
	a := &resumeFirmwareAdapter{pairingAdapter: &pairingAdapter{
		status: domain.Status{Model: registered.Model, ModelFamily: "pico-s-pro", DeviceUID: "uid", Ready: true},
		caps:   domain.Capabilities{Model: registered.Model, ModelFamily: "pico-s-pro", HubOTAV1: true, FirmwareVersion: "1.0.0"},
	}, stages: make(chan string, 1)}
	d := s.attach(registered, nil)
	d.adapter = a
	if _, err := s.verifiedStatus(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	install := func() (int, []byte) {
		return request(t, c, "POST", h.URL+"/api/v1/devices/brewer/firmware/install", csrf,
			map[string]any{"package_id": pkg.ID, "device_uid": "uid", "confirm": true}, nil)
	}
	a.caps.FirmwareVersion = pkg.FirmwareVersion
	if code, body := install(); code != 409 || !bytes.Contains(body, []byte("firmware_already_installed")) {
		t.Fatalf("same-version install: %d %s", code, body)
	}
	var receipt firmware.InstallReceipt
	if _, err := s.db.Get("firmware-install", registered.ID, &receipt); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unexpected install receipt: %+v, %v", receipt, err)
	}
	// Deliberate downgrades still work, even when the cached view matches the package.
	a.caps.FirmwareVersion = "2.0.0"
	if code, body := install(); code != 202 {
		t.Fatalf("downgrade: %d %s", code, body)
	}
}

// The real revised archive is the authorization boundary. Exercise it when
// running the repository integration suite; the standalone Hub checkout has no firmware tree.
func TestPicoSOriginalReceiptOffersRevisedBuildOnce(t *testing.T) {
	root := os.Getenv("PICO_FIRMWARE_LIBRARY")
	if root == "" {
		t.Skip("repository version library is outside the standalone Hub")
	}
	archive, err := os.ReadFile(filepath.Join(root, "pico-s-pro", "modified", "releases", "pico-s", "1.0.0.zip"))
	if err != nil {
		t.Fatal(err)
	}
	s, h, client, stop := startFirmwareServer(t, newTestDataDir(t))
	defer stop()
	csrf := signIn(t, client, h.URL)
	code, body := uploadFirmware(t, client, h.URL, csrf, archive, nil)
	if code != 201 {
		t.Fatalf("upload: %d %s", code, body)
	}
	pkg := parseFirmwarePackage(t, body)
	const original = "f0d4798eb31ce28d7df870b0e29e592e0362c1ac93da307f550a0acf8856bf6d"
	const uid = "test-brewer"
	registered := domain.Device{ID: "brewer", Model: "pico-s", ModelSource: "firmware", Enabled: true, Adapter: "pico-standalone"}
	a := &resumeFirmwareAdapter{pairingAdapter: &pairingAdapter{
		status: domain.Status{Model: registered.Model, ModelFamily: "pico-s-pro", DeviceUID: uid, Ready: true},
		caps:   domain.Capabilities{Model: registered.Model, ModelFamily: "pico-s-pro", HubOTAV1: true, FirmwareVersion: "1.0.0"},
	}, stages: make(chan string, 1)}
	d := s.attach(registered, nil)
	d.adapter = a
	if _, err := s.verifiedStatus(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	path := h.URL + "/api/v1/devices/brewer/firmware"
	install := func() (int, []byte) {
		return request(t, client, "POST", path+"/install", csrf, map[string]any{"package_id": pkg.ID, "device_uid": uid, "confirm": true}, nil)
	}
	checkOffer := func(want string) {
		t.Helper()
		code, data := request(t, client, "GET", path, "", nil, nil)
		var result firmware.MachineFirmware
		if code != 200 || json.Unmarshal(data, &result) != nil || result.RevisedPackageID != want {
			t.Fatalf("wrong revised package offer: %d %s", code, data)
		}
	}
	checkDenied := func() {
		t.Helper()
		checkOffer("")
		if code, body := install(); code != 409 || !bytes.Contains(body, []byte("firmware_already_installed")) {
			t.Fatalf("same version accepted: %d %s", code, body)
		}
	}
	save := func(receipt firmware.InstallReceipt) {
		t.Helper()
		if _, err := s.db.Put("firmware-install", registered.ID, receipt, -1); err != nil {
			t.Fatal(err)
		}
	}
	checkDenied()
	prior := firmware.InstallReceipt{UID: "other-brewer", Model: "pico-s", PackageID: original, State: "installed"}
	save(prior)
	checkDenied()
	prior.UID = uid
	prior.Model = "pico-pro"
	save(prior)
	checkDenied()
	prior.Model = "pico-s"
	save(prior)
	checkOffer(pkg.ID)
	if code, data := install(); code != 202 {
		t.Fatalf("eligible revision rejected: %d %s", code, data)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var receipt firmware.InstallReceipt
		if _, err := s.db.Get("firmware-install", registered.ID, &receipt); err != nil {
			t.Fatal(err)
		}
		d.mu.RLock()
		active := d.updatingFirmware
		d.mu.RUnlock()
		if !active && receipt.State == "installed" && receipt.PackageID == pkg.ID && receipt.ReplacesPackageID == original {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("revised package was not confirmed: %+v", receipt)
		}
		time.Sleep(10 * time.Millisecond)
	}
	checkDenied()
}

func TestInterruptedFirmwareKeepsReceiptAndCanResume(t *testing.T) {
	for _, stage := range []string{"entering", "transferring", "selecting", "confirming"} {
		t.Run(stage, func(t *testing.T) { testInterruptedFirmwareRetry(t, stage) })
	}
}

func testInterruptedFirmwareRetry(t *testing.T, stage string) {
	s, h, c, stop := startFirmwareServer(t, newTestDataDir(t))
	defer stop()
	csrf := signIn(t, c, h.URL)
	files := firmwaretest.Files("pico-s")
	firmwaretest.ChangeReport(files, func(report map[string]any) { report["hub_ota_protocol"] = 1 })
	code, body := uploadFirmware(t, c, h.URL, csrf, firmwaretest.Archive(files), nil)
	if code != 201 {
		t.Fatalf("upload: %d %s", code, body)
	}
	original := parseFirmwarePackage(t, body).ID
	const uid = "brewer-uid"
	saved := domain.Device{ID: "brewer", Model: "pico-s", ModelSource: "firmware", Enabled: true, Adapter: "pico-standalone"}
	status := domain.Status{Model: saved.Model, ModelFamily: "pico-s-pro", DeviceUID: uid, Ready: true}
	caps := domain.Capabilities{Model: saved.Model, ModelFamily: "pico-s-pro", FirmwareVersion: "2.0.0", HubOTAV1: true}
	a := &resumeFirmwareAdapter{pairingAdapter: &pairingAdapter{status: status, caps: caps}, stages: make(chan string, 1)}
	d := s.attach(saved, nil)
	d.adapter = a // No saved key in this fixture, so attach starts no poller.
	if _, err := s.verifiedStatus(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	receipt := firmware.InstallReceipt{PackageID: original, Model: saved.Model, UID: uid, State: "unconfirmed", Stage: stage}
	version, err := s.db.Put("firmware-install", saved.ID, receipt, 0)
	if err != nil {
		t.Fatal(err)
	}
	path := h.URL + "/api/v1/devices/brewer/firmware"
	checkRetry := func(expected bool) {
		t.Helper()
		code, body := request(t, c, "GET", path, "", nil, nil)
		var machine firmware.MachineFirmware
		if code != 200 || json.Unmarshal(body, &machine) != nil || machine.InstallSupported != expected ||
			(expected && (machine.RetryPackageID != original || machine.DeviceUID != uid)) || (!expected && machine.RetryPackageID != "") {
			t.Fatalf("retry available %t: %d %s", expected, code, body)
		}
	}
	install := func(id string) int {
		t.Helper()
		code, _ := request(t, c, "POST", path+"/install", csrf, map[string]any{"package_id": id, "device_uid": uid, "confirm": true}, nil)
		return code
	}
	checkRetry(true)
	if code := install(strings.Repeat("b", 64)); code != 409 {
		t.Fatalf("different package accepted during %s: %d", stage, code)
	}
	var after firmware.InstallReceipt
	if v, err := s.db.Get("firmware-install", saved.ID, &after); err != nil || v != version || after != receipt {
		t.Fatalf("rejected package replaced %s checkpoint: %+v, %d, %v", stage, after, v, err)
	}

	if stage == "entering" || stage == "transferring" {
		// In-place recovery can keep reporting normal application status.
		// Respect a live busy brewer and resume the original stage once idle.
		a.status.Busy = true
		if _, err := s.verifiedStatus(context.Background(), d); err != nil {
			t.Fatal(err)
		}
		checkRetry(false)
		if code := install(original); code != 409 {
			t.Fatalf("busy brewer accepted a retry: %d", code)
		}
		if v, err := s.db.Get("firmware-install", saved.ID, &after); err != nil || v != version || after != receipt {
			t.Fatalf("busy retry changed the checkpoint: %+v, %d, %v", after, v, err)
		}
		a.status.Busy = false
		if _, err := s.verifiedStatus(context.Background(), d); err != nil {
			t.Fatal(err)
		}
		checkRetry(true)
		if code := install(original); code != 202 {
			t.Fatalf("online same-package retry: %d", code)
		}
		select {
		case got := <-a.stages:
			if got != stage {
				t.Fatalf("online retry lost its stage: %q", got)
			}
		case <-time.After(time.Second):
			t.Fatal("adapter did not receive online retry")
		}
		return
	}

	// A positive UID mismatch must block even an exact-package retry.
	a.status.DeviceUID = "different"
	if _, err := s.verifiedStatus(context.Background(), d); err == nil {
		t.Fatal("expected identity mismatch")
	}
	checkRetry(false)
	if code := install(original); code != 409 {
		t.Fatalf("different brewer accepted: %d", code)
	}

	// Recreate runtime state as on startup: an offline application in recovery
	// cannot provide /status. The saved identity and interrupted package survive.
	restarted := s.attach(saved, nil)
	restarted.adapter = a
	a.statusErr = errors.New("application offline")
	if _, err := s.verifiedStatus(context.Background(), restarted); err == nil {
		t.Fatal("expected offline application")
	}
	if err := s.db.Delete("device-identity", saved.ID); err != nil {
		t.Fatal(err)
	}
	checkRetry(false)
	if code := install(original); code != 409 {
		t.Fatalf("missing identity pin accepted: %d", code)
	}
	if _, err := s.db.Put("device-identity", saved.ID, deviceIdentity{UID: uid}, 0); err != nil {
		t.Fatal(err)
	}
	checkRetry(true)
	if code := install(original); code != 202 {
		t.Fatalf("same package could not resume: %d", code)
	}
	select {
	case stage := <-a.stages:
		if stage != receipt.Stage {
			t.Fatalf("lost original stage: %q", stage)
		}
	case <-time.After(time.Second):
		t.Fatal("adapter did not receive retry")
	}
}

func TestPicoCInstallationRequiresIndependentlyVerifiedController(t *testing.T) {
	s, h, c, stop := startFirmwareServer(t, newTestDataDir(t))
	defer stop()
	csrf := signIn(t, c, h.URL)
	uid := "c-uid"
	model := "pico-c"
	a := &resumeFirmwareAdapter{pairingAdapter: &pairingAdapter{
		status: domain.Status{Model: model, ModelFamily: model, DisplayVariant: "ssd1325", DeviceUID: uid, Ready: true},
		caps:   domain.Capabilities{Model: model, ModelFamily: model, DisplayVariant: "ssd1325", HubOTAV1: true, FirmwareVersion: "1.0.0"},
	}, stages: make(chan string, 1)}
	d := s.attach(domain.Device{ID: "brewer", Model: model, ModelSource: "firmware", Enabled: true, Adapter: "pico-standalone"}, nil)
	d.adapter = a
	if _, err := s.verifiedStatus(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	url := h.URL + "/api/v1/devices/brewer/firmware"
	packages := map[string]string{}
	for _, display := range []string{"ssd1309", "ssd1325"} {
		code, body := uploadFirmware(t, c, h.URL, csrf, firmwaretest.Archive(firmwaretest.COTAFiles(display)), nil)
		if code != 201 {
			t.Fatalf("upload: %d %s", code, body)
		}
		packages[display] = parseFirmwarePackage(t, body).ID
	}
	install := func(display string, want int) {
		t.Helper()
		code, body := request(t, c, "POST", url+"/install", csrf, map[string]any{"package_id": packages[display], "device_uid": uid, "confirm": true}, nil)
		if code != want {
			t.Fatalf("install %s: %d %s", display, code, body)
		}
	}
	verify := func(display string) {
		code, body := request(t, c, "PUT", url+"/hardware", csrf, map[string]string{"device_uid": uid, "display_variant": display}, nil)
		if code != 200 {
			t.Fatalf("verify %s: %d %s", display, code, body)
		}
	}
	install("ssd1325", 409)
	verify("ssd1309")
	install("ssd1309", 409)
	install("ssd1325", 409)
	verify("ssd1325")
	install("ssd1309", 409)
	install("ssd1325", 202)
	select {
	case <-a.stages:
	case <-time.After(time.Second):
		t.Fatal("install did not start")
	}
}
