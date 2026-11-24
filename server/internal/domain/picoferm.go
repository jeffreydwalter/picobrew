package domain

func (d Device) IsPicoFerm() bool {
	return d.Adapter == "pico-standalone" && d.Model == "picoferm"
}

func (d Device) IsBrewer() bool {
	if d.Adapter != "pico-standalone" {
		return false
	}
	switch d.Model {
	case "pico-s", "pico-pro", "pico-c", "z-series", "zymatic":
		return true
	}
	return false
}

// A manual registration can later identify itself as a different model.
func (d *Device) ClearInapplicableBrewer() {
	if d.BrewerID != "" && !d.IsPicoFerm() {
		d.BrewerID = ""
		d.BrewerRevision++
	}
}

// Fermentation batches come from a brewer's recorded brewing sessions. Cleaning,
// diagnostics, simulator data and accessory measurements have different meanings.
func (b Brew) IsFermentationBatch() bool {
	return b.Source == "device" && (b.Command == "brew" || b.Command == "manual")
}

func (b Brew) IsPicoFermRecording() bool {
	return b.Model == "picoferm" && b.Source == "device" && b.Command == "start"
}
