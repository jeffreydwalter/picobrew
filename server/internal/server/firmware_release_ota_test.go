package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/firmware"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/firmware/firmwaretest"
)

func TestReleaseChoicesExcludeNonOTAFirmwareFromOldAndRefreshedCatalogs(t *testing.T) {
	s, h, client, stop := startFirmwareServer(t, newTestDataDir(t))
	defer stop()
	csrf := signIn(t, client, h.URL)
	commit := strings.Repeat("a", 40)
	makeRelease := func(version string, protocol int) (firmware.Release, []byte) {
		files := firmwaretest.Files("pico-s")
		firmwaretest.ChangeReport(files, func(r map[string]any) {
			r["firmware_version"], r["hub_ota_protocol"] = version, protocol
		})
		data := firmwaretest.Archive(files)
		c, err := firmware.ValidateArchive(data)
		if err != nil {
			t.Fatal(err)
		}
		return firmware.Release{Candidate: c, ArchiveSHA256: firmware.Digest(data), Kind: "standalone", SourceCommit: commit}, data
	}
	downgrade, archive := makeRelease("1.3.0", 1)
	update, _ := makeRelease("1.3.4", 1)
	usb, _ := makeRelease("1.4.0", 0)
	history := firmware.Release{Candidate: firmware.Candidate{Model: "pico-s-pro", ModelFamily: "pico-s-pro", FirmwareVersion: "1.5.0", BinaryBytes: 1, BinarySHA256: strings.Repeat("1", 64)}, Kind: "historical", SourceCommit: commit, ArchiveSHA256: strings.Repeat("2", 64)}
	ref := firmware.Release{Candidate: firmware.Candidate{Model: "z-series", ModelFamily: "z-series", FirmwareVersion: "2.0.0", BinaryBytes: 1, BinarySHA256: strings.Repeat("3", 64)}, Kind: "reference", SourceCommit: commit, ArchiveSHA256: strings.Repeat("4", 64)}
	pro := update
	pro.Model = "pico-pro"
	pro.ArchiveSHA256 = strings.Repeat("5", 64)
	releases := []firmware.Release{downgrade, history, usb, ref, pro, update}
	catalog := firmware.ReleaseCatalog{Settings: firmware.Settings{Repository: firmware.DefaultRepository}, Commit: commit, Checked: time.Now().UTC(), Releases: releases}
	if _, err := s.db.Put("config", firmwareCatalogKey, catalog, 0); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"pico-s", "z-series"} {
		d := &runtimeDevice{cancel: func() {}, view: domain.DeviceView{Device: domain.Device{ID: model, Name: model, Adapter: "pico-standalone", Model: model}, Capabilities: &domain.Capabilities{Model: model, FirmwareVersion: "1.3.3"}}}
		s.mu.Lock()
		s.devices[model] = d
		s.mu.Unlock()
	}
	index := firmwaretest.JSON(map[string]any{"schema_version": 1, "releases": releases})
	fixtures := map[string][]byte{"releases.json": index, firmware.DefaultRepository: []byte(`{"default_branch":"master"}`), "commits/master": []byte(`{"sha":"` + commit + `"}`), downgrade.FirmwareVersion + ".zip": archive}
	var downloads int
	s.firmwareGitHub.HTTP = &http.Client{Transport: firmwareTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, ".zip") {
			downloads++
		}
		return fixtureGitHubResponse(r, fixtures), nil
	})}
	for _, method := range []string{"GET", "POST"} {
		code, b := request(t, client, method, h.URL+"/api/v1/firmware/updates", csrf, nil, nil)
		var got releaseView
		if code != 200 || json.Unmarshal(b, &got) != nil || len(got.Releases) != 2 || got.Releases[0] != downgrade || got.Releases[1] != update || len(got.Notices) != 1 || got.Notices[0].Version != update.FirmwareVersion || got.Notices[0].DeviceID != "pico-s" {
			t.Fatalf("%s firmware choices: %d %s", method, code, b)
		}
		for _, r := range []firmware.Release{history, usb, ref} {
			code, b = request(t, client, "POST", h.URL+"/api/v1/firmware/releases/"+r.ArchiveSHA256+"/stage", csrf, nil, nil)
			if code != 404 || downloads != 0 {
				t.Fatalf("staging %s: %d %s (downloads %d)", r.FirmwareVersion, code, b, downloads)
			}
		}
	}
	var saved firmware.ReleaseCatalog
	if _, err := s.db.Get("config", firmwareCatalogKey, &saved); err != nil || len(saved.Releases) != 3 {
		t.Fatal("refresh retained non-OTA releases", saved, err)
	}
	for range 2 {
		code, b := request(t, client, "POST", h.URL+"/api/v1/firmware/releases/"+downgrade.ArchiveSHA256+"/stage", csrf, nil, nil)
		if code != 200 || parseFirmwarePackage(t, b).Candidate != downgrade.Candidate || downloads != 1 {
			t.Fatalf("compatible downgrade stage: %d %s; downloads %d", code, b, downloads)
		}
	}
}

func TestStagingRechecksLegacyCachedArchiveAgainstOTAMetadata(t *testing.T) {
	s, h, c, stop := startFirmwareServer(t, newTestDataDir(t))
	defer stop()
	csrf := signIn(t, c, h.URL)
	files := firmwaretest.Files("pico-s")
	firmwaretest.ChangeReport(files, func(r map[string]any) { delete(r, "hub_ota_protocol") })
	archive := firmwaretest.Archive(files)
	candidate, err := firmware.ValidateArchive(archive)
	if err != nil {
		t.Fatal(err)
	}
	// Model a stale package on disk and an index that claims the old image supports OTA.
	candidate.HubOTAProtocol = 1
	id := firmware.Digest(archive)
	r := firmware.Release{Candidate: candidate, Kind: "standalone", ArchiveSHA256: id, SourceCommit: strings.Repeat("a", 40)}
	if _, err = s.db.Put("config", firmwareCatalogKey, firmware.ReleaseCatalog{Settings: firmware.Settings{Repository: firmware.DefaultRepository}, Commit: r.SourceCommit, Releases: []firmware.Release{r}}, 0); err != nil {
		t.Fatal(err)
	}
	if err = s.ensureFirmwareCache(); err != nil {
		t.Fatal(err)
	}
	if err = s.publishFirmwareArchive(id, archive); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Put(firmwareRecordKind, id, firmware.Package{ID: id, Candidate: candidate}, 0); err != nil {
		t.Fatal(err)
	}
	s.firmwareGitHub.HTTP = &http.Client{Transport: firmwareTransport(func(*http.Request) (*http.Response, error) {
		t.Error("unexpected download")
		return nil, context.Canceled
	})}
	code, b := request(t, c, "POST", h.URL+"/api/v1/firmware/releases/"+id+"/stage", csrf, nil, nil)
	if code != 422 {
		t.Fatalf("non-OTA archive with conflicting staged metadata: %d %s", code, b)
	}
}

func TestESPReleasesRequireAMatchingDeviceRecoveryProtocol(t *testing.T) {
	for _, tc := range []struct{ model, protocol, kind string }{
		{"picostill", firmware.ESP32FactoryProtocol, "reference"},
		{"z-series", firmware.ESP32FactoryProtocol, "reference"},
		{"picoferm", firmware.FermABProtocol, "picoferm-ab"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			wireless := firmware.Release{Kind: tc.kind, Candidate: firmware.Candidate{
				Model: tc.model, ModelFamily: tc.model, OTAProtocol: tc.protocol, BinaryBytes: 400000,
			}}
			if !wireless.MatchesWirelessTarget(tc.model, "") || wireless.MatchesWirelessTarget(tc.model, "ssd1309") ||
				wireless.MatchesWirelessTarget(tc.model, "ssd1325") != (tc.model == "z-series") {
				t.Fatal("package accepted the wrong hardware identity")
			}
			usb := wireless
			usb.OTAProtocol = ""
			wrongKind := wireless
			wrongKind.Kind = "standalone"
			filtered := otaCatalog(firmware.ReleaseCatalog{Releases: []firmware.Release{usb, wireless, wrongKind}})
			if len(filtered.Releases) != 1 || filtered.Releases[0] != wireless {
				t.Fatal("incorrect remote release choices", filtered.Releases)
			}
			v := domain.DeviceView{Device: domain.Device{Adapter: "pico-standalone", Model: tc.model, Enabled: true, ModelSource: "firmware"}, Online: true,
				Status: &domain.Status{Ready: true, DeviceUID: "abcd"}, Capabilities: &domain.Capabilities{Model: tc.model, OTAProtocol: tc.protocol}}
			if !otaEligible(v) {
				t.Fatal("provisioned idle device was excluded")
			}
			v.Status.Busy = true
			if otaEligible(v) {
				t.Fatal("busy device was eligible")
			}
			v.Status.Busy = false
			v.Capabilities.OTAProtocol = ""
			if otaEligible(v) {
				t.Fatal("USB-only device was eligible")
			}
		})
	}
}

func TestFirmwareUpdatesAcrossPublicVersionReset(t *testing.T) {
	target := firmware.Release{Candidate: firmware.Candidate{FirmwareVersion: "1.0.0"}}
	for _, tc := range []struct {
		source, channel, version string
		want                     bool
	}{
		{"stable", "", "1.3.9", true},
		{"stable", "", "1.0.0", false}, // Same-number updates require an exact prior receipt and replacement release.
		{"stable", "", "2.0.0", false},
		{"stable", "", "1.4.0", false},
		{"stable", "", "preview-090fe78", true},
		{"stable", "stable", "1.0.0", false},
		{"stable", "stable", "1.3.9", false},
		{"", "", "1.3.9", false},
		{"stable", "", "", false},
	} {
		c := firmware.ReleaseCatalog{ReleaseChannel: tc.source}
		d := &domain.Capabilities{ReleaseChannel: tc.channel, FirmwareVersion: tc.version}
		if got := updateAvailable(c, target, d); got != tc.want {
			t.Errorf("catalog %q, device %q %s: got %t", tc.source, tc.channel, tc.version, got)
		}
	}
}
