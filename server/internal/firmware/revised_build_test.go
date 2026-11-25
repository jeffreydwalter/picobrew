package firmware

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPinnedBetaReplacementIdentities(t *testing.T) {
	for _, target := range betaReplacements {
		t.Run(target.model+target.display, func(t *testing.T) {
			for _, old := range target.prior {
				receipt := InstallReceipt{UID: "device", Model: target.model, DisplayVariant: target.display, State: "installed", PackageID: old.archive}
				if got := receipt.RevisedPackageID("device", target.model, target.display, "1.0.0"); got != target.current.archive {
					t.Fatal("missing revision", got)
				}
				if receipt.RevisedPackageID("other", target.model, target.display, "1.0.0") != "" ||
					receipt.RevisedPackageID("device", target.model, target.display+"other", "1.0.0") != "" ||
					receipt.RevisedPackageID("device", target.model, target.display, "1.0.1") != "" {
					t.Fatal("mismatched identity was eligible")
				}
				receipt.PackageID = target.current.archive
				if receipt.RevisedPackageID("device", target.model, target.display, "1.0.0") != "" {
					t.Fatal("offered repeat installation")
				}
			}
		})
	}
}

func TestZRevisionUsesPhysicalDisplayIdentity(t *testing.T) {
	for _, target := range betaReplacements {
		if target.model != "z-series" {
			continue
		}
		for _, old := range target.prior {
			r := InstallReceipt{UID: "z", Model: "z-series", DisplayVariant: "ssd1325", PackageID: old.archive, State: "installed"}
			if r.RevisedPackageID("z", "z-series", "ssd1325", "1.0.0") != target.current.archive ||
				r.RevisedPackageID("z", "z-series", "", "1.0.0") != "" {
				t.Fatalf("Z release and physical display receipt were not distinguished")
			}
			r.DisplayVariant = "ssd1309"
			if r.RevisedPackageID("z", "z-series", "ssd1309", "1.0.0") != "" {
				t.Fatalf("unknown Z display was eligible")
			}
		}
		return
	}
	t.Fatal("missing Z beta release")
}
func TestRevisionPinsMatchReleasedImages(t *testing.T) {
	root := os.Getenv("PICO_FIRMWARE_LIBRARY")
	if root == "" {
		t.Skip("repository firmware library is outside the standalone Hub")
	}
	b, err := os.ReadFile(filepath.Join(root, "releases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index struct{ Releases []Release }
	if err = json.Unmarshal(b, &index); err != nil {
		t.Fatal(err)
	}
	for _, target := range betaReplacements {
		found := false
		for _, r := range index.Releases {
			if r.Model != target.model || r.DisplayVariant != target.display || r.FirmwareVersion != "1.0.0" {
				continue
			}
			p := UpdatePayload{Candidate: r.Candidate, ArchiveSHA256: r.ArchiveSHA256}
			for _, old := range target.prior {
				p.ReplacesPackageID = old.archive
				if !p.ReplacesRunningImage(old.application) || p.ReplacesRunningImage(r.BinarySHA256) {
					t.Fatalf("wrong image authorization for %s %s", r.Model, r.DisplayVariant)
				}
			}
			found = true
		}
		if !found {
			t.Fatal("missing published target", target.model, target.display)
		}
	}
}
