package usbservice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/privatefs"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
)

const fakePort = "ch340-serial-0123456789abcdef0123456789abcdef"
const promptOne = "aaaaaaaaaaaaaaaaaaaaaaaa"
const promptTwo = "bbbbbbbbbbbbbbbbbbbbbbbb"

func TestWorkerProcess(t *testing.T) {
	mode := os.Getenv("PICOBREW_TEST_JOB_WORKER")
	if mode == "" {
		return
	}
	d := json.NewDecoder(os.Stdin)
	e := json.NewEncoder(os.Stdout)
	var req map[string]any
	if d.Decode(&req) != nil {
		os.Exit(2)
	}
	if req["action"] == "list" {
		_ = e.Encode(event{Type: "devices", Devices: []USBDevice{{Port: fakePort, Label: "Test adapter", Models: []string{"pico-s"}}}})
	} else {
		if req["expected_uid"] != "machine-from-pin" || !filepath.IsAbs(req["data_dir"].(string)) || len(req["job_id"].(string)) != 32 {
			os.Exit(3)
		}
		if mode == "offline" {
			program, ok := req["offline_program"].(map[string]any)
			if !ok || program["model"] != "zymatic" || program["name"] != "Local ale" || req["hub_host"] != "hub.lan:8080" {
				os.Exit(7)
			}
		}
		for _, id := range []string{promptOne, promptTwo} {
			_ = e.Encode(event{Type: "confirm", ID: id, Message: "Confirm local service step."})
			var ans USBAnswer
			if d.Decode(&ans) != nil || ans.Confirm != id {
				_ = e.Encode(event{Type: "error", Message: "Canceled."})
				os.Exit(4)
			}
		}
		if mode == "interrupted" {
			os.Exit(0)
		}
	}
	_ = e.Encode(event{Type: "complete", Message: "Controller verified."})
	if mode == "bad-exit" && req["action"] != "list" {
		os.Exit(5)
	}
	os.Exit(0)
}
func newJobs(t *testing.T, mode string) *Manager {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	if err := privatefs.EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "hub.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	m, err := Open(dir, db, false)
	if err != nil {
		t.Fatal(err)
	}
	m.available = true
	m.command = func(ctx context.Context, _ string) (*exec.Cmd, error) {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkerProcess$")
		cmd.Env = append(os.Environ(), "PICOBREW_TEST_JOB_WORKER="+mode)
		return cmd, nil
	}
	t.Cleanup(m.Close)
	return m
}
func withPrompt(t *testing.T, m *Manager, id, want string) USBJob {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	var j USBJob
	for time.Now().Before(deadline) {
		j, _ = m.Get(id)
		if j.State == want || j.Confirmation == want {
			return j
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("did not reach %s: %#v", want, j)
	return j
}
func TestWorkerBoundPromptsAndExit(t *testing.T) {
	for _, mode := range []string{"ok", "interrupted", "bad-exit", "cancel", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			m := newJobs(t, mode)
			info := m.Devices(context.Background())
			if !info.Available || len(info.Devices) != 1 || info.Devices[0].Port != fakePort {
				t.Fatalf("enumeration: %#v", info)
			}
			in := USBInput{Action: "restore", Port: fakePort, Model: "pico-s", DeviceID: "my-machine"}
			j, err := m.Start(in, "machine-from-pin", "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = m.Start(in, "machine-from-pin", ""); !errors.Is(err, ErrBusy) {
				t.Fatal(err)
			}
			withPrompt(t, m, j.ID, promptOne)
			reload := m.Devices(context.Background())
			if reload.Job == nil || reload.Job.Confirmation != promptOne {
				t.Fatal(reload)
			}
			if _, err = m.Answer(j.ID, promptTwo, true); !errors.Is(err, ErrPrompt) {
				t.Fatal(err)
			}
			if mode == "shutdown" {
				m.Close()
				withPrompt(t, m, j.ID, "canceled")
				return
			}
			if _, err = m.Answer(j.ID, promptOne, mode != "cancel"); err != nil {
				t.Fatal(err)
			}
			if mode == "cancel" {
				withPrompt(t, m, j.ID, "canceled")
				return
			}
			withPrompt(t, m, j.ID, promptTwo)
			if _, err = m.Answer(j.ID, promptOne, true); !errors.Is(err, ErrPrompt) {
				t.Fatal(err)
			}
			if _, err = m.Answer(j.ID, promptTwo, true); err != nil {
				t.Fatal(err)
			}
			want := "complete"
			if mode != "ok" {
				want = "failed"
			}
			j = withPrompt(t, m, j.ID, want)
			if j.Confirmation != "" {
				t.Fatal("stale prompt")
			}
			if _, err = m.Answer(j.ID, promptTwo, true); !errors.Is(err, ErrPrompt) {
				t.Fatal(err)
			}
			m.Close()
			restored, err := Open(m.dir, m.db, false)
			if err != nil {
				t.Fatal(err)
			}
			j, err = restored.Get(j.ID)
			if err != nil || j.State != want {
				t.Fatal(j, err)
			}
		})
	}
}
func TestWorkerRestartAndInvalidInputs(t *testing.T) {
	m := newJobs(t, "ok")
	var in = USBInput{Action: "restore", Model: "pico-s", Port: "/dev/ttyUSB0"}
	if _, err := m.Start(in, "", ""); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	in.Port = fakePort
	in.Model = "pico-c"
	if _, err := m.Start(in, "", ""); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	stale := USBJob{ID: strings.Repeat("1", 32), State: "running", CreatedAt: time.Now()}
	if err := m.persist(stale); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(m.dir, m.db, false)
	if err != nil {
		t.Fatal(err)
	}
	v, err := restarted.Get(stale.ID)
	if err != nil || v.State != "failed" {
		t.Fatal(v, err)
	}
	stale.State = "complete"
	if err = m.persist(stale); err != nil {
		t.Fatal(err)
	}
	if j, err := restarted.Get(stale.ID); err != nil || j.State != "complete" {
		t.Fatal(j, err)
	}
	if info := restarted.Devices(context.Background()); info.Available || info.Job == nil || info.Job.State != "complete" {
		t.Fatal(info)
	}
}

func TestPairingReadyRequiresVerifiedInstallAndPrivateHandoff(t *testing.T) {
	m := newJobs(t, "ok")
	id := strings.Repeat("1", 32)
	j := USBJob{ID: id, State: "complete", USBInput: USBInput{Action: "install", Model: "pico-s"}, CreatedAt: time.Now()}
	if err := m.persist(j); err != nil {
		t.Fatal(err)
	}
	got, err := m.Get(id)
	if err != nil || got.PairingReady {
		t.Fatal(got, err)
	}
	dir := filepath.Join(m.dir, "usb-jobs", id)
	if err = privatefs.EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("ab", 32)
	uid := strings.Repeat("cd", 16)
	write := func(name, value string) {
		t.Helper()
		f, err := privatefs.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString(value + "\n")
		if err != nil {
			t.Fatal(err)
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	write("device-key", key)
	write("device-uid", uid)
	got, err = m.Get(id)
	if err != nil || !got.PairingReady {
		t.Fatal(got, err)
	}
	wire, _ := json.Marshal(got)
	if strings.Contains(string(wire), key) || strings.Contains(string(wire), uid) {
		t.Fatal("credential leaked")
	}
	// Both the UI flag and registration consume the same model-specific USB
	// result. Reading credentials here must agree with what the UI offered.
	for _, model := range models {
		j.Model = model
		if err := m.persist(j); err != nil {
			t.Fatal(err)
		}
		esp := model != "pico-s" && model != "pico-pro" && model != "pico-c" && model != "zymatic"
		for _, sample := range []string{uid, uid[:12], "0123456789gg", uid[:10]} {
			write("device-uid", sample)
			got, err := m.Get(id)
			want := esp && sample == uid[:12] || !esp && sample == uid
			if err != nil || got.PairingReady != want {
				t.Fatalf("%s %q: ready=%v, error=%v", model, sample, got.PairingReady, err)
			}
			gotKey, gotUID, err := m.InstalledCredential(id)
			if (err == nil) != want || want && (gotKey != key || gotUID != sample) ||
				!want && (gotKey != "" || gotUID != "") {
				t.Fatalf("%s %q: credential result did not match readiness", model, sample)
			}
		}
	}
	write("device-uid", uid)
	j.Model, j.State = "zymatic", "failed"
	if err = m.persist(j); err != nil {
		t.Fatal(err)
	}
	if got, err = m.Get(id); err != nil || got.PairingReady {
		t.Fatal("accepted an unverified install")
	}
}

func TestOfflineProgramUSBAdmission(t *testing.T) {
	const raw = `{"model":"zymatic","name":"Local ale","id":"0123456789abcdef0123456789abcdef","steps":[{"name":"Mash","location":1,"target_f":152,"minutes":50,"drain_minutes":0}]}`
	for _, bad := range []string{strings.Replace(raw, `"drain_minutes":0`, `"drain_minutes":null`, 1),
		strings.Replace(raw, `"target_f":152,`, "", 1), strings.Replace(raw, `"location":1`, `"slot":1`, 1)} {
		var program OfflineProgram
		if json.Unmarshal([]byte(bad), &program) == nil {
			t.Fatal("accepted missing or invalid step field")
		}
	}
	var program OfflineProgram
	if err := json.Unmarshal([]byte(raw), &program); err != nil {
		t.Fatal(err)
	}
	m := newJobs(t, "offline")
	input := USBInput{Action: "install", Model: "zymatic", Port: fakePort, OfflineProgram: &program}
	for _, variation := range []struct{ action, model string }{{"restore", "zymatic"}, {"install", "pico-s"}} {
		bad := input
		bad.Action = variation.action
		bad.Model = variation.model
		if _, err := m.Start(bad, "machine-from-pin", ""); !errors.Is(err, ErrInput) {
			t.Fatal(err)
		}
	}
	program.Steps[0].Location = 6
	if _, err := m.Start(input, "machine-from-pin", ""); !errors.Is(err, ErrInput) {
		t.Fatal("accepted terminal pause")
	}
	program.Steps[0].Location = 1
	job, err := m.Start(input, "machine-from-pin", "hub.lan:8080")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{promptOne, promptTwo} {
		withPrompt(t, m, job.ID, id)
		if _, err = m.Answer(job.ID, id, true); err != nil {
			t.Fatal(err)
		}
	}
	withPrompt(t, m, job.ID, "complete")
}
