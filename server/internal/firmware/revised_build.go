package firmware

// The pre-release and final packages share version 1.0.0. Only these exact
// archive pairs may reuse a version, and the device must also identify the
// expected running application before the Hub enters recovery.
type releaseBuild struct {
	archive, application string
}

type replacement struct {
	model, display string
	current        releaseBuild
	prior          []releaseBuild
}

func previousBuild(model, display, archive string) (prior, current releaseBuild, ok bool) {
	for _, target := range betaReplacements {
		if target.model == model && target.display == display {
			for _, old := range target.prior {
				if old.archive == archive {
					return old, target.current, true
				}
			}
		}
	}
	return
}

func (r InstallReceipt) RevisedPackageID(uid, model, display, version string) string {
	if uid == "" || uid != r.UID || r.State != "installed" || r.Model != model ||
		r.DisplayVariant != display || version != "1.0.0" {
		return ""
	}
	_, current, _ := previousBuild(model, display, r.PackageID)
	return current.archive
}

func (p UpdatePayload) IsApprovedRevision() bool {
	_, current, ok := previousBuild(p.Model, p.DisplayVariant, p.ReplacesPackageID)
	return ok && (p.SupportsCC3200OTA() || p.SupportsFactoryOTA()) && p.FirmwareVersion == "1.0.0" &&
		current.archive == p.ArchiveSHA256 && current.application == p.BinarySHA256
}

func (p UpdatePayload) ReplacesRunningImage(digest string) bool {
	old, _, ok := previousBuild(p.Model, p.DisplayVariant, p.ReplacesPackageID)
	return ok && old.application == digest && p.IsApprovedRevision()
}
