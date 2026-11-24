package firmware

// The initial and revised public Pico S builds both identify themselves as 1.0.0.
// These archive IDs cover the application, website and package metadata.
const (
	picoSOriginalPackage = "f0d4798eb31ce28d7df870b0e29e592e0362c1ac93da307f550a0acf8856bf6d"
	picoSOriginalApp     = "5b221f2f52afceab4c9fe7d1c391071b708f439d9e1be8cf883addef584852af"
	picoSRevisedPackage  = "6c10f4f546687da07ef748573db3c5ed26294391edc192dfdee7c9abc6cd6c8b"
	picoSRevisedApp      = "d92b1b073ada25ef62ebbb7c501fdf5d1a220393c873d838d480f3e1a59d7801"
)

func (r InstallReceipt) RevisedPackageID(uid, model, version string) string {
	if uid != "" && uid == r.UID && r.State == "installed" && r.PackageID == picoSOriginalPackage &&
		r.Model == "pico-s" && model == r.Model && version == "1.0.0" && r.DisplayVariant == "" {
		return picoSRevisedPackage
	}
	return ""
}

func (p UpdatePayload) IsPicoSRevision() bool {
	return p.ReplacesPackageID == picoSOriginalPackage && p.ArchiveSHA256 == picoSRevisedPackage &&
		p.BinarySHA256 == picoSRevisedApp && p.FirmwareVersion == "1.0.0" && p.Model == "pico-s" && p.SupportsCC3200OTA()
}

func (p UpdatePayload) ReplacesRunningImage(digest string) bool {
	return p.IsPicoSRevision() && digest == picoSOriginalApp
}
