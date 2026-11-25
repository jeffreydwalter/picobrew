// Package usbservice runs one local USB firmware operation at a time. Only the
// bundled worker has access to serial ports and the private firmware captures.
package usbservice

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/privatefs"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/recipe"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/usbservice/helper"
)

var (
	ErrBusy        = errors.New("A USB operation is already in progress.")
	ErrUnavailable = errors.New("USB service is unavailable in this Hub build.")
	ErrMissing     = errors.New("USB job not found.")
	ErrPrompt      = errors.New("That USB prompt is no longer active. Refresh the job.")
	ErrInput       = errors.New("Choose a connected USB adapter and a supported model, action and display.")
	portPattern    = regexp.MustCompile("^(serial|ch340-serial|ch340-usb)-[0-9a-f]{32}$")
	promptPattern  = regexp.MustCompile("^[0-9a-f]{24}$")
	models         = []string{"pico-s", "pico-pro", "pico-c", "zymatic", "z-series", "z1", "z2", "z3", "z4", "picostill", "picoferm"}
)

type USBDevice struct {
	Port   string   `json:"port"`
	Label  string   `json:"label"`
	Models []string `json:"models"`
}
type USBDevices struct {
	Available bool        `json:"available"`
	Message   string      `json:"message,omitempty"`
	Devices   []USBDevice `json:"devices"`
	Job       *USBJob     `json:"job,omitempty"`
}
type OfflineProgram struct {
	Model string `json:"model"`
	domain.MachineRecipe
}

// Keep a missing route, temperature or duration from silently becoming zero.
func (p *OfflineProgram) UnmarshalJSON(data []byte) error {
	type wire OfflineProgram
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode((*wire)(p)); err != nil {
		return err
	}
	var source struct {
		Steps []map[string]*json.RawMessage `json:"steps"`
	}
	if err := json.Unmarshal(data, &source); err != nil {
		return err
	}
	for i, step := range source.Steps {
		for _, field := range []string{"location", "target_f", "minutes", "drain_minutes"} {
			if step[field] == nil {
				return fmt.Errorf("step %d requires %s", i+1, field)
			}
		}
	}
	return nil
}

type USBInput struct {
	OfflineProgram *OfflineProgram `json:"offline_program,omitempty"`
	Action         string          `json:"action"`
	Model          string          `json:"model"`
	Port           string          `json:"port"`
	Display        string          `json:"display,omitempty"`
	DeviceID       string          `json:"device_id,omitempty"`
}
type USBJob struct {
	ID string `json:"id"`
	USBInput
	State        string    `json:"state"`
	Message      string    `json:"message"`
	Confirmation string    `json:"confirmation,omitempty"`
	PairingReady bool      `json:"pairing_ready,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}
type USBAnswer struct {
	Confirm string `json:"confirm"`
}

type event struct {
	Type    string      `json:"type"`
	Message string      `json:"message"`
	ID      string      `json:"id"`
	Devices []USBDevice `json:"devices"`
}
type managerCommand func(context.Context, string) (*exec.Cmd, error)
type Manager struct {
	dir                      string
	db                       *store.Store
	available                bool
	command                  managerCommand
	mu                       sync.Mutex
	wg                       sync.WaitGroup
	closed, active, canceled bool
	inherited                bool
	last                     *USBJob
	reply                    io.WriteCloser
	timer                    *time.Timer
	probeMu                  sync.Mutex
}

func Open(dir string, db *store.Store, enabled bool) (*Manager, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	m := &Manager{dir: dir, db: db, available: enabled && helper.Available(), command: helper.Command}
	rows, err := db.List("usb-job")
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		var j USBJob
		if err = json.Unmarshal(row, &j); err != nil {
			return nil, err
		}
		if m.last == nil || j.CreatedAt.After(m.last.CreatedAt) {
			m.last = &j
			m.inherited = j.State == "running" || j.State == "waiting"
		}
	}
	return m, nil
}
func (m *Manager) persist(j USBJob) error {
	_, err := m.db.Put("usb-job", j.ID, j, -1)
	return err
}

// InstalledCredential retrieves the private handoff for a verified USB installation.
func (m *Manager) InstalledCredential(id string) (key, uid string, err error) {
	j, err := m.readJob(id)
	if err != nil || j.ID != id {
		return "", "", ErrMissing
	}
	return m.installCredential(j)
}

func (m *Manager) installCredential(j USBJob) (key, uid string, err error) {
	if j.State != "complete" || j.Action != "install" || len(j.ID) != 32 || j.ID != strings.ToLower(j.ID) {
		return "", "", errors.New("no verified USB installation")
	}
	if _, err := hex.DecodeString(j.ID); err != nil {
		return "", "", errors.New("invalid USB job")
	}

	// The ESP devices identify themselves by a six-byte MAC. The CC3200
	// and Zymatic installers provision a sixteen-byte device identity.
	var uidBytes int
	switch j.Model {
	case "picoferm", "picostill", "z-series", "z1", "z2", "z3", "z4":
		uidBytes = 6
	case "pico-s", "pico-pro", "pico-c", "zymatic":
		uidBytes = 16
	default:
		return "", "", errors.New("unknown USB device model")
	}
	dir := filepath.Join(m.dir, "usb-jobs", j.ID)
	rawUID, err := privatefs.ReadFile(filepath.Join(dir, "device-uid"), 34)
	if err != nil {
		return "", "", err
	}
	rawKey, err := privatefs.ReadFile(filepath.Join(dir, "device-key"), 66)
	if err != nil {
		return "", "", err
	}
	uid = strings.TrimSuffix(strings.TrimSuffix(string(rawUID), "\n"), "\r")
	key = strings.TrimSuffix(strings.TrimSuffix(string(rawKey), "\n"), "\r")
	decodedUID, uidErr := hex.DecodeString(uid)
	decodedKey, keyErr := hex.DecodeString(key)
	if uidErr != nil || len(decodedUID) != uidBytes || keyErr != nil || len(decodedKey) != 32 ||
		key == strings.Repeat("0", 64) || strings.EqualFold(key, strings.Repeat("f", 64)) {
		return "", "", errors.New("invalid USB pairing result")
	}
	return key, uid, nil
}

func (m *Manager) withPair(j USBJob) USBJob {
	_, _, err := m.installCredential(j)
	j.PairingReady = err == nil
	return j
}
func (m *Manager) current() *USBJob {
	if m.last == nil {
		return nil
	}
	j := *m.last
	if m.inherited {
		if disk, err := m.readJob(j.ID); err == nil {
			j = disk
		}
	}
	copy := m.withPair(j)
	return &copy
}
func (m *Manager) storedJob(id string) (USBJob, error) {
	m.mu.Lock()
	if m.last != nil && m.last.ID == id && !m.inherited {
		j := *m.last
		m.mu.Unlock()
		return j, nil
	}
	m.mu.Unlock()
	return m.readJob(id)
}
func (m *Manager) readJob(id string) (USBJob, error) {
	var j USBJob
	_, err := m.db.Get("usb-job", id, &j)
	// Another process can still own the worker and commit the real outcome.
	// This instance never replays its prompt or overwrites its durable state.
	if err == nil && (j.State == "running" || j.State == "waiting") {
		j.State = "failed"
		j.Confirmation = ""
		j.Message = "This Hub process has no verified completion. If another Hub is open, check its job before retrying."
	}
	if errors.Is(err, store.ErrNotFound) {
		err = ErrMissing
	}
	return j, err
}

func (m *Manager) Get(id string) (USBJob, error) {
	j, err := m.storedJob(id)
	if err != nil {
		return j, err
	}
	return m.withPair(j), nil
}

// execute always drains the worker and checks its exit status. Unstructured
// process output never reaches a browser or the Hub logs.
func (m *Manager) execute(ctx context.Context, request any, onStart func(io.WriteCloser), accept func(event) bool) bool {
	cmd, err := m.command(ctx, m.dir)
	if err != nil {
		return false
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		return false
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		return false
	}
	defer output.Close()
	if err = cmd.Start(); err != nil {
		return false
	}
	ok := json.NewEncoder(input).Encode(request) == nil
	if ok && onStart != nil {
		onStart(input)
	} else {
		_ = input.Close()
	}
	s := bufio.NewScanner(output)
	s.Buffer(make([]byte, 1024), 32768)
	for s.Scan() {
		var ev event
		if !ok || json.Unmarshal(s.Bytes(), &ev) != nil || !accept(ev) {
			ok = false
			_ = input.Close()
		}
	}
	_ = input.Close()
	if s.Err() != nil {
		_ = output.Close()
		ok = false
	}
	return cmd.Wait() == nil && ok
}

func (m *Manager) Devices(ctx context.Context) USBDevices {
	info := USBDevices{Devices: []USBDevice{}, Available: m.available}
	m.mu.Lock()
	info.Job = m.current()
	active := m.active || m.closed
	m.mu.Unlock()
	if !m.available {
		info.Message = ErrUnavailable.Error()
		return info
	}
	if active {
		return info
	}
	if !m.probeMu.TryLock() {
		info.Message = "USB devices are being refreshed."
		return info
	}
	defer m.probeMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	seen, complete := false, false
	ok := m.execute(ctx, map[string]string{"action": "list"}, nil, func(ev event) bool {
		switch ev.Type {
		case "devices":
			if seen || complete || len(ev.Devices) > 256 {
				return false
			}
			for _, d := range ev.Devices {
				if !portPattern.MatchString(d.Port) || len(d.Label) > 160 || len(d.Models) == 0 {
					return false
				}
				for _, v := range d.Models {
					if !slices.Contains(models, v) {
						return false
					}
				}
			}
			info.Devices = append(info.Devices, ev.Devices...)
			seen = true
		case "complete":
			if !seen || complete {
				return false
			}
			complete = true
		default:
			return false
		}
		return true
	})
	if !ok || !complete {
		info.Devices = []USBDevice{}
		info.Available = false
		info.Message = "The Hub could not list USB adapters. Check the Hub computer’s USB access and try again."
	}
	return info
}

func (m *Manager) Start(in USBInput, expectedUID, hubHost string) (USBJob, error) {
	if in.OfflineProgram != nil {
		if in.Action != "install" || in.Model != "zymatic" || in.OfflineProgram.Model != "zymatic" {
			return USBJob{}, fmt.Errorf("%w Offline programs require a Zymatic installation.", ErrInput)
		}
		if err := recipe.ValidateStockZymaticProgram(in.OfflineProgram.MachineRecipe); err != nil {
			return USBJob{}, fmt.Errorf("%w %s", ErrInput, err)
		}
	}
	if !m.available {
		return USBJob{}, ErrUnavailable
	}
	if !slices.Contains([]string{"backup", "install", "restore"}, in.Action) || !slices.Contains(models, in.Model) || !portPattern.MatchString(in.Port) ||
		(in.Model == "pico-c" && in.Display != "ssd1309" && in.Display != "ssd1325") || (in.Model != "pico-c" && in.Display != "") ||
		(in.DeviceID != "" && expectedUID == "") {
		return USBJob{}, ErrInput
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active || m.closed {
		return USBJob{}, ErrBusy
	}
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	j := USBJob{ID: hex.EncodeToString(nonce[:]), USBInput: in, State: "running", Message: "Checking the selected USB adapter.", CreatedAt: time.Now().UTC()}
	if err := m.persist(j); err != nil {
		return USBJob{}, err
	}
	m.active = true
	m.inherited = false
	m.canceled = false
	m.last = &j
	m.wg.Add(1)
	go m.run(j, expectedUID, hubHost)
	return j, nil
}
func (m *Manager) run(j USBJob, uid, host string) {
	defer m.wg.Done()
	req := map[string]any{"job_id": j.ID, "action": j.Action, "model": j.Model, "port": j.Port, "display": j.Display, "data_dir": m.dir}
	if uid != "" {
		req["expected_uid"] = uid
	}
	if j.OfflineProgram != nil {
		req["offline_program"] = j.OfflineProgram
	}
	if host != "" && j.Model == "zymatic" && j.Action == "install" {
		req["hub_host"] = host
	}
	complete, reported := "", ""
	ok := m.execute(context.Background(), req, func(w io.WriteCloser) {
		m.mu.Lock()
		m.reply = w
		m.mu.Unlock()
	}, func(ev event) bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		if complete != "" || reported != "" || m.last.State == "waiting" || strings.TrimSpace(ev.Message) == "" || len(ev.Message) > 1024 {
			return false
		}
		switch ev.Type {
		case "progress":
			m.last.Message = ev.Message
		case "confirm":
			if !promptPattern.MatchString(ev.ID) || m.canceled {
				return false
			}
			m.last.State = "waiting"
			m.last.Confirmation = ev.ID
			m.last.Message = ev.Message
			if m.closed {
				m.answerLocked(ev.ID, false)
			} else {
				m.timer = time.AfterFunc(30*time.Minute, func() { _, _ = m.Answer(j.ID, ev.ID, false) })
			}
		case "complete":
			complete = ev.Message
		case "error":
			reported = ev.Message
		default:
			return false
		}
		return true
	})
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	m.last.Confirmation = ""
	m.reply = nil
	switch {
	case m.canceled:
		m.last.State = "canceled"
		m.last.Message = "USB operation canceled at the confirmation prompt."
	case ok && complete != "":
		m.last.State = "complete"
		m.last.Message = complete
	default:
		m.last.State = "failed"
		m.last.Message = reported
		if reported == "" {
			m.last.Message = "The USB worker stopped before it verified completion. Read the machine again before retrying."
		}
	}
	if err := m.persist(*m.last); err != nil {
		m.last.Message += " The Hub could not save the final job status."
	}
	m.active = false
}
func (m *Manager) answerLocked(prompt string, approve bool) error {
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	answer := ""
	if approve {
		answer = prompt
	} else {
		m.canceled = true
	}
	m.last.State = "running"
	m.last.Confirmation = ""
	m.last.Message = "Waiting for the USB worker."
	if err := json.NewEncoder(m.reply).Encode(USBAnswer{Confirm: answer}); err != nil {
		_ = m.reply.Close()
		return errors.New("The USB worker disconnected before receiving this answer.")
	}
	return nil
}
func (m *Manager) Answer(id, prompt string, approve bool) (USBJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.active || m.last.ID != id || m.last.State != "waiting" || m.last.Confirmation != prompt || !promptPattern.MatchString(prompt) || m.reply == nil {
		return USBJob{}, ErrPrompt
	}
	err := m.answerLocked(prompt, approve)
	return *m.last, err
}

// Close cancels unanswered prompts but allows an accepted write and its readback
// to finish before the Hub closes its database.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	if m.active && m.last.State == "waiting" {
		_ = m.answerLocked(m.last.Confirmation, false)
	}
	m.mu.Unlock()
	m.wg.Wait()
}
