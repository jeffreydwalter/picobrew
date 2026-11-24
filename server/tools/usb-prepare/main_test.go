package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStagingRejectsMismatchedBytesAndKeepsVerifiedWorker(t *testing.T) {
	input, output := t.TempDir(), t.TempDir()
	host := runtime.GOOS + "-" + runtime.GOARCH
	name := "picobrew-usb-" + host
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	worker := filepath.Join(input, name)
	metadata := filepath.Join(input, host+".json")
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := func(content []byte) {
		t.Helper()
		digest := sha256.Sum256(content)
		data, err := json.Marshal(Asset{Platform: host, Name: name, Bytes: int64(len(content)), SHA256: hex.EncodeToString(digest[:])})
		if err != nil {
			t.Fatal(err)
		}
		write(metadata, data)
	}
	original := []byte("first worker")
	write(worker, original)
	manifest(original)
	if err := stage(input, output); err != nil {
		t.Fatal(err)
	}
	manifest([]byte("next worker"))
	write(worker, []byte("bad! worker"))
	if err := stage(input, output); err == nil {
		t.Fatal("accepted worker with an incorrect release digest")
	}
	data, err := os.ReadFile(filepath.Join(output, "worker"))
	if err != nil || string(data) != string(original) {
		t.Fatalf("verified worker changed: %v", err)
	}

	// A fresh checkout needs its input once; subsequent builds use the
	// already verified payload even if the local build input is gone.
	manifest(original)
	if err := os.Remove(worker); err != nil {
		t.Fatal(err)
	}
	if err := stage(input, output); err != nil {
		t.Fatal(err)
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCandidateOnlyFillsMissingPinnedProductionWorker(t *testing.T) {
	originalRelease, originalTransport := releases, http.DefaultTransport
	t.Cleanup(func() { releases, http.DefaultTransport = originalRelease, originalTransport })
	host := runtime.GOOS + "-" + runtime.GOARCH
	name := "picobrew-usb-" + host
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	content := "candidate worker approved for stable release"
	sum := sha256.Sum256([]byte(content))
	index := Index{Tag: "v1.0.0", CandidateTag: "usb-candidate-123-1", Assets: map[string]Asset{
		host: {Platform: host, Name: name, Bytes: int64(len(content)), SHA256: hex.EncodeToString(sum[:])},
	}}
	var err error
	releases, err = json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name           string
		candidateFirst bool
		stable         int
		stableData     string
		candidateData  string
		wantRequests   int
		shouldSucceed  bool
	}{
		{"stable", false, 200, content, "", 1, true},
		{"before publication", false, 404, "", content, 2, true},
		{"corrupt stable", false, 200, "wrong", content, 1, false},
		{"corrupt candidate", false, 404, "", "wrong", 2, false},
		{"failed stable download", false, 503, "", content, 1, false},
		{"before tag replacement", true, 200, "historical worker", content, 1, true},
		{"after candidate retirement", true, 200, content, "", 2, true},
		{"replacement has bad digest", true, 200, content, "wrong", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index.PreferCandidate = tc.candidateFirst
			releases, err = json.Marshal(index)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			http.DefaultTransport = transportFunc(func(r *http.Request) (*http.Response, error) {
				count++
				tag, code, data := index.Tag, tc.stable, tc.stableData
				if (count == 2) != tc.candidateFirst {
					tag, code, data = index.CandidateTag, 200, tc.candidateData
					if data == "" {
						code = 404
					}
				}
				expected := "/jeffreydwalter/picobrew/releases/download/" + tag + "/" + name
				if r.URL.Host != "github.com" || r.URL.Path != expected {
					t.Fatalf("unexpected release URL %s", r.URL)
				}
				return &http.Response{StatusCode: code, Status: http.StatusText(code), Body: io.NopCloser(strings.NewReader(data)), Header: make(http.Header), Request: r}, nil
			})
			err := stage("", t.TempDir())
			if (err == nil) != tc.shouldSucceed || count != tc.wantRequests {
				t.Fatalf("stage returned %v after %d requests", err, count)
			}
		})
	}
}

func TestStageForPlatformSelectsTargetWorker(t *testing.T) {
	input, output := t.TempDir(), t.TempDir()
	const target = "linux-arm"
	const name = "picobrew-usb-" + target
	content := []byte("arm worker payload")
	digest := sha256.Sum256(content)
	if err := os.WriteFile(filepath.Join(input, name), content, 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(Asset{
		Platform: target,
		Name:     name,
		Bytes:    int64(len(content)),
		SHA256:   hex.EncodeToString(digest[:]),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, target+".json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := stageForPlatform(target, input, output); err != nil {
		t.Fatalf("stageForPlatform(%q) returned error: %v", target, err)
	}
	stagedManifest, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got Asset
	if err := json.Unmarshal(stagedManifest, &got); err != nil {
		t.Fatal(err)
	}
	if got.Platform != target {
		t.Fatalf("manifest platform = %q, want %q", got.Platform, target)
	}
	if err := stageForPlatform("../bad", input, output); err == nil {
		t.Fatal("accepted invalid platform string")
	}
}
