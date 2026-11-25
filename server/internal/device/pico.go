package device

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

const maxRequestBytes = 4095
const maxResponseBytes = 64 << 10

type pico struct {
	base            *url.URL
	key             string
	client          *http.Client
	gate            chan struct{}
	capabilities    *domain.Capabilities
	historyFields   []string
	historyMetadata map[string]domain.FieldMetadata
}

var apiKey = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
var errorCode = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func NewPico(baseURL, key string) (Adapter, error) { return newPico(baseURL, key, false) }

// allowLoopback is private and exists only for local protocol tests. Real device
// registration must use NewPico; the simulator needs no HTTP endpoint.
func newPico(baseURL, key string, allowLoopback bool) (*pico, error) {
	if !apiKey.MatchString(key) {
		return nil, invalid("Device API key must contain exactly 64 hexadecimal characters")
	}
	p, err := newPicoTransport(baseURL, allowLoopback)
	if err == nil {
		p.key = key
	}
	return p, err
}

func newPicoTransport(baseURL string, allowLoopback bool) (*pico, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base == nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || base.Opaque != "" || (base.Path != "" && base.Path != "/") || base.RawPath != "" {
		return nil, invalid("Device URL must contain only an HTTP(S) scheme, hostname and optional port")
	}
	host := strings.TrimSuffix(strings.ToLower(base.Hostname()), ".")
	if strings.ContainsAny(host, "% \\") || (!allowLoopback && (host == "localhost" || strings.HasSuffix(host, ".localhost"))) {
		return nil, invalid("Device URL uses a prohibited address")
	}
	if ip := net.ParseIP(host); ip != nil && prohibitedIP(ip, allowLoopback) {
		return nil, invalid("Device URL uses a prohibited address")
	}
	if port := base.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return nil, invalid("Device port must be from 1 to 65535")
		}
	}
	base.Path = ""
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxConnsPerHost: 1, ForceAttemptHTTP2: false, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("invalid device address")
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, errors.New("device hostname could not be resolved")
		}
		for _, address := range addresses {
			if prohibitedIP(address.IP, allowLoopback) {
				return nil, errors.New("device hostname resolved to a prohibited address")
			}
		}
		for _, address := range addresses {
			connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.IP.String(), port))
			if err == nil {
				return connection, nil
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		return nil, errors.New("device connection failed")
	}
	return &pico{base: base, gate: make(chan struct{}, 1), client: &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func prohibitedIP(ip net.IP, allowLoopback bool) bool {
	return ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || (!allowLoopback && ip.IsLoopback()) || ip.Equal(net.ParseIP("100.100.100.200"))
}

func (p *pico) lock(ctx context.Context) error {
	if ctx.Err() != nil {
		return &Error{Code: "cancelled", Message: "Device request was canceled before dispatch"}
	}
	select {
	case p.gate <- struct{}{}:
		if ctx.Err() != nil {
			p.unlock()
			return &Error{Code: "cancelled", Message: "Device request was canceled while queued"}
		}
		return nil
	case <-ctx.Done():
		return &Error{Code: "cancelled", Message: "Device request was canceled while queued"}
	}
}
func (p *pico) unlock() { <-p.gate }

func (p *pico) request(ctx context.Context, method, path string, value, destination any, statuses ...int) error {
	var body []byte
	var err error
	write := method != http.MethodGet
	if value != nil {
		body, err = json.Marshal(value)
		if err != nil {
			return invalid("Device request could not be encoded")
		}
		if len(body) > maxRequestBytes {
			return &Error{StatusCode: 413, Code: "payload_too_large", Message: "Device request exceeds 4095 bytes"}
		}
	}
	endpoint := *p.base
	parts := strings.SplitN(path, "?", 2)
	endpoint.Path = parts[0]
	if len(parts) == 2 {
		endpoint.RawQuery = parts[1]
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return invalid("Device request could not be prepared")
	}
	if p.key != "" {
		request.Header.Set("Authorization", "Bearer "+p.key)
	}
	request.Header.Set("Accept", "application/json")
	request.Close = true
	if write {
		request.Header.Set("Content-Type", "application/json")
		request.ContentLength = int64(len(body))
	}
	response, err := p.client.Do(request)
	if err != nil {
		return &Error{Code: "transport_error", Message: "Device did not return a complete response", UnknownOutcome: write}
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if readErr != nil || len(data) > maxResponseBytes {
		return &Error{StatusCode: response.StatusCode, Code: "invalid_response", Message: "Device response is incomplete or exceeds its size limit", UnknownOutcome: write}
	}
	accepted := false
	for _, status := range statuses {
		if response.StatusCode == status {
			accepted = true
			break
		}
	}
	if !accepted {
		failure := struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}{}
		_ = json.Unmarshal(data, &failure)
		if !errorCode.MatchString(failure.Error) || p.cleanMessage(failure.Error) != failure.Error {
			failure.Error = "device_rejected"
		}
		message := p.cleanMessage(failure.Message)
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		if message == "" {
			message = "Device returned an unexpected status"
		}
		return &Error{StatusCode: response.StatusCode, Code: failure.Error, Message: message, UnknownOutcome: write && (response.StatusCode >= 500 || response.StatusCode == 408 || response.StatusCode >= 200 && response.StatusCode < 400)}
	}
	if destination != nil {
		if err := json.Unmarshal(data, destination); err != nil {
			return &Error{StatusCode: response.StatusCode, Code: "invalid_response", Message: "Device returned invalid JSON", UnknownOutcome: write}
		}
	}
	return nil
}

func (p *pico) cleanMessage(message string) string {
	for _, secret := range []string{p.key, strings.ToLower(p.key), strings.ToUpper(p.key), p.base.String(), p.base.Host, p.base.Hostname()} {
		if secret != "" {
			message = regexp.MustCompile("(?i)"+regexp.QuoteMeta(secret)).ReplaceAllString(message, "[redacted]")
		}
	}
	message = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, message)
	if len(message) > 256 {
		message = message[:256]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	return strings.TrimSpace(message)
}

func (p *pico) discover(ctx context.Context) (domain.Capabilities, error) {
	var reply struct {
		domain.Capabilities
		Mode string `json:"mode"`
	}
	if err := p.request(ctx, http.MethodGet, "/api/v1", nil, &reply, 200); err != nil {
		return domain.Capabilities{}, err
	}
	if reply.APIVersion != 1 || reply.Mode != "standalone" {
		return domain.Capabilities{}, &Error{Code: "unsupported_firmware", Message: "Device does not expose the supported standalone API v1"}
	}
	capabilities := reply.Capabilities
	if _, err := ReportedModel(capabilities); err != nil {
		p.capabilities = nil
		return domain.Capabilities{}, err
	}
	// The AVR overlay has no device-preference endpoint. An omitted list on
	// early CC3200 firmware, in contrast, means the original three settings.
	if capabilities.Model == "zymatic" && capabilities.SettingsFields == nil {
		capabilities.SettingsFields = []string{}
	}
	p.capabilities = &capabilities
	result := capabilities
	result.Commands = append([]string(nil), capabilities.Commands...)
	// The Hub exposes these dedicated device resources through its confirmed,
	// idempotent command endpoint. Keep the cached wire contract intact.
	if capabilities.SystemRestart {
		result.Commands = append(result.Commands, "restart")
	}
	if capabilities.RestoreDefaultRecipes {
		result.Commands = append(result.Commands, "restore-starter-recipes")
	}
	return result, nil
}
func (p *pico) Discover(ctx context.Context) (domain.Capabilities, error) {
	if err := p.lock(ctx); err != nil {
		return domain.Capabilities{}, err
	}
	defer p.unlock()
	return p.discover(ctx)
}
func (p *pico) Status(ctx context.Context) (domain.Status, error) {
	var result domain.Status
	if err := p.lock(ctx); err != nil {
		return result, err
	}
	defer p.unlock()
	err := p.request(ctx, http.MethodGet, "/api/v1/status", nil, &result, 200)
	if err == nil {
		err = validateRecovery(result.Recovery)
	}
	return result, err
}
func (p *pico) Recipes(ctx context.Context) ([]domain.MachineRecipe, error) {
	if err := p.lock(ctx); err != nil {
		return nil, err
	}
	defer p.unlock()
	if p.capabilities != nil && !p.capabilities.Supports("recipes") {
		return nil, unsupportedCapability("recipes")
	}
	var list struct {
		Recipes []struct {
			ID string `json:"id"`
		} `json:"recipes"`
	}
	if err := p.request(ctx, http.MethodGet, "/api/v1/recipes", nil, &list, 200); err != nil {
		return nil, err
	}
	if len(list.Recipes) > 16 {
		return nil, &Error{Code: "invalid_response", Message: "Device recipe inventory exceeds capacity"}
	}
	result := make([]domain.MachineRecipe, 0, len(list.Recipes))
	for _, summary := range list.Recipes {
		recipe, err := p.readRecipe(ctx, summary.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, recipe)
	}
	return result, nil
}
func (p *pico) Recipe(ctx context.Context, id string) (domain.MachineRecipe, error) {
	if err := p.lock(ctx); err != nil {
		return domain.MachineRecipe{}, err
	}
	defer p.unlock()
	if p.capabilities != nil && !p.capabilities.Supports("recipes") {
		return domain.MachineRecipe{}, unsupportedCapability("recipes")
	}
	return p.readRecipe(ctx, id)
}

// readRecipe reads one slot while the caller holds the adapter lock.
func (p *pico) readRecipe(ctx context.Context, id string) (domain.MachineRecipe, error) {
	slot, err := p.readRecipeSlot(ctx, id)
	return slot.MachineRecipe, err
}

type recipeSlot struct {
	domain.MachineRecipe
	Revision uint32 `json:"revision"`
}

func (p *pico) readRecipeSlot(ctx context.Context, id string) (recipeSlot, error) {
	if !recipeID.MatchString(id) {
		return recipeSlot{}, &Error{Code: "invalid_response", Message: "Invalid machine recipe ID"}
	}
	var recipe recipeSlot
	if err := p.request(ctx, http.MethodGet, "/api/v1/recipes/"+id, nil, &recipe, 200); err != nil {
		return recipeSlot{}, err
	}
	if recipe.ZProgram != nil {
		recipe.Name = recipe.ZProgram.Name
	}
	if recipe.ID != id || validateRecipe(recipe.MachineRecipe) != nil {
		return recipeSlot{}, &Error{Code: "invalid_response", Message: "Device returned an invalid recipe"}
	}
	return recipe, nil
}

// Compare the contents the operator saw, then use the revision from that same read for the write.
func (p *pico) checkedRecipeRevision(ctx context.Context, id string, expected *domain.MachineRecipe) (uint32, error) {
	slot, err := p.readRecipeSlot(ctx, id)
	if err != nil {
		var missing *Error
		if expected != nil && errors.As(err, &missing) && missing.StatusCode == 404 {
			return 0, recipeChanged()
		}
		return 0, err
	}
	if expected != nil && !reflect.DeepEqual(slot.MachineRecipe, *expected) {
		return 0, recipeChanged()
	}
	if p.capabilities != nil && p.capabilities.RecipeConditionalWrite {
		if slot.Revision == 0 {
			return 0, &Error{Code: "invalid_response", Message: "The brewer omitted the recipe revision required for saving changes."}
		}
		return slot.Revision, nil
	}
	return 0, nil
}

func (p *pico) CreateRecipe(ctx context.Context, recipe domain.MachineRecipe) error {
	return p.writeRecipe(ctx, recipe, true, nil)
}
func (p *pico) PutRecipe(ctx context.Context, recipe domain.MachineRecipe, expected *domain.MachineRecipe) error {
	return p.writeRecipe(ctx, recipe, false, expected)
}
func (p *pico) writeRecipe(ctx context.Context, recipe domain.MachineRecipe, create bool, expected *domain.MachineRecipe) error {
	if err := validateRecipe(recipe); err != nil {
		return err
	}
	if err := p.lock(ctx); err != nil {
		return err
	}
	defer p.unlock()
	if p.capabilities != nil && !p.capabilities.Supports("recipes") {
		return unsupportedCapability("recipes")
	}
	path := "/api/v1/recipes/" + recipe.ID
	var createOnly bool
	var revision uint32
	accepted := []int{200, 201}
	zeroMinute := false
	for _, step := range recipe.Steps {
		if step.Minutes == 0 {
			zeroMinute = true
			break
		}
	}
	if create || zeroMinute || recipe.ZProgram != nil || expected != nil {
		capabilities, err := p.discover(ctx)
		if err != nil {
			return err
		}
		if !capabilities.Supports("recipes") {
			return unsupportedCapability("recipes")
		}
		if recipe.ZProgram != nil && (capabilities.Model != "z-series" || !capabilities.ZProgramMaker || recipe.StepCount() > capabilities.StepCapacity) {
			return unsupportedCapability("z_program_maker")
		}
		if zeroMinute && !capabilities.ZeroMinuteSteps {
			return &Error{StatusCode: 409, Code: "unsupported_zero_minute_steps", Message: "This firmware does not advertise zero-minute heating steps. Update the brewer firmware before sending the original program."}
		}
		if create {
			if capabilities.RecipeCreateOnly {
				accepted = []int{201}
				createOnly = true
			} else {
				// Older firmware has only an upsert. Recheck this specific slot under the
				// adapter lock immediately before writing. Independent device clients can
				// still race this fallback on firmware without atomic create support.
				err := p.request(ctx, http.MethodGet, path, nil, nil, 200)
				if err == nil {
					return existsError()
				}
				var missing *Error
				if !errors.As(err, &missing) || missing.StatusCode != 404 || (missing.Code != "not_found" && missing.Code != "recipe_not_found") {
					return err
				}
			}
		}
	}
	if expected != nil {
		var err error
		revision, err = p.checkedRecipeRevision(ctx, recipe.ID, expected)
		if err != nil {
			return err
		}
		accepted = []int{200}
	}
	var body any = struct {
		domain.MachineRecipe
		CreateOnly bool   `json:"create_only,omitempty"`
		IfRevision uint32 `json:"if_revision,omitempty"`
	}{recipe, createOnly, revision}
	if recipe.ZProgram != nil {
		body = struct {
			ID         string           `json:"id"`
			Z          *domain.ZProgram `json:"z_program"`
			CreateOnly bool             `json:"create_only,omitempty"`
			IfRevision uint32           `json:"if_revision,omitempty"`
		}{recipe.ID, recipe.ZProgram, createOnly, revision}
		recipe.Steps = nil
	}
	if err := p.request(ctx, http.MethodPut, path, body, nil, accepted...); err != nil {
		return err
	}
	var observed domain.MachineRecipe
	if err := p.request(ctx, http.MethodGet, path, nil, &observed, 200); err != nil {
		return &Error{Code: "verification_failed", Message: "Recipe write was accepted but readback failed", UnknownOutcome: true}
	}
	if observed.ZProgram != nil {
		observed.Name = observed.ZProgram.Name
	}
	if !reflect.DeepEqual(recipe, observed) {
		return &Error{Code: "verification_failed", Message: "Recipe readback does not match the intended recipe", UnknownOutcome: true}
	}
	return nil
}
func (p *pico) DeleteRecipe(ctx context.Context, id string, expected *domain.MachineRecipe) error {
	if !recipeID.MatchString(id) {
		return invalid("Recipe ID is invalid")
	}
	if err := p.lock(ctx); err != nil {
		return err
	}
	defer p.unlock()
	if p.capabilities != nil && !p.capabilities.Supports("recipes") {
		return unsupportedCapability("recipes")
	}
	var body any
	if expected != nil || p.capabilities != nil && p.capabilities.RecipeConditionalWrite {
		if p.capabilities == nil {
			if _, err := p.discover(ctx); err != nil {
				return err
			}
		}
		revision, err := p.checkedRecipeRevision(ctx, id, expected)
		if err != nil {
			return err
		}
		if revision != 0 {
			body = struct {
				IfRevision uint32 `json:"if_revision"`
			}{revision}
		}
	}
	return p.request(ctx, http.MethodDelete, "/api/v1/recipes/"+id, body, nil, 200, 204)
}
func (p *pico) Command(ctx context.Context, command domain.Command) error {
	if err := p.lock(ctx); err != nil {
		return err
	}
	defer p.unlock()
	if p.capabilities == nil {
		if _, err := p.discover(ctx); err != nil {
			return err
		}
	}
	switch command.Command {
	case "restart", "restore-starter-recipes":
		if command != (domain.Command{Command: command.Command}) {
			return invalid("This operation takes no device arguments")
		}
		if command.Command == "restart" {
			if !p.capabilities.SystemRestart {
				return unsupportedCapability("system_restart")
			}
			var reply struct {
				Accepted bool `json:"accepted"`
			}
			if err := p.request(ctx, http.MethodPost, "/api/v1/system/restart", struct{}{}, &reply, 202); err != nil {
				return err
			}
			if !reply.Accepted {
				return &Error{Code: "invalid_response", Message: "Restart acknowledgement missing", UnknownOutcome: true}
			}
		} else {
			if !p.capabilities.RestoreDefaultRecipes {
				return unsupportedCapability("restore_default_recipes")
			}
			var reply struct{ Added, Preserved, Capacity *int }
			if err := p.request(ctx, http.MethodPost, "/api/v1/recipes/restore-defaults", nil, &reply, 200); err != nil {
				return err
			}
			if reply.Added == nil || reply.Preserved == nil || reply.Capacity == nil || *reply.Capacity != p.capabilities.RecipeCapacity || *reply.Added < 0 || *reply.Preserved < 0 || *reply.Added > *reply.Capacity || *reply.Preserved > *reply.Capacity-*reply.Added {
				return &Error{Code: "invalid_response", Message: "Starter recipe restoration could not be verified", UnknownOutcome: true}
			}
		}
		return nil
	}
	allowed := false
	for _, value := range p.capabilities.Commands {
		if value == command.Command {
			allowed = true
			break
		}
	}
	if !allowed {
		return &Error{StatusCode: 400, Code: "unsupported_command", Message: "Device did not advertise this command"}
	}
	if err := validateDiscoveredCommand(command, *p.capabilities); err != nil {
		return err
	}
	var reply struct {
		Accepted bool   `json:"accepted"`
		Command  string `json:"command"`
	}
	if err := p.request(ctx, http.MethodPost, "/api/v1/commands", command, &reply, 202); err != nil {
		return err
	}
	if !reply.Accepted || reply.Command != command.Command {
		return &Error{Code: "invalid_response", Message: "Command acceptance could not be verified", UnknownOutcome: true}
	}
	return nil
}
func (p *pico) readMap(ctx context.Context, resource string) (map[string]any, error) {
	if err := p.lock(ctx); err != nil {
		return nil, err
	}
	defer p.unlock()
	if p.capabilities != nil {
		switch resource {
		case "settings":
			if p.capabilities.SettingsFields != nil && len(p.capabilities.SettingsFields) == 0 {
				return nil, unsupportedCapability("device preferences")
			}
		case "telemetry":
			if !p.capabilities.Supports("telemetry_forwarding") {
				return nil, unsupportedCapability("telemetry forwarding")
			}
		}
	}
	var result map[string]any
	err := p.request(ctx, http.MethodGet, "/api/v1/"+resource, nil, &result, 200)
	return result, err
}
func (p *pico) Settings(ctx context.Context) (map[string]any, error) {
	return p.readMap(ctx, "settings")
}
func (p *pico) Telemetry(ctx context.Context) (map[string]any, error) {
	return p.readMap(ctx, "telemetry")
}
func (p *pico) UpdateSettings(ctx context.Context, settings map[string]any) error {
	if err := validateSettings(settings); err != nil {
		return err
	}
	if err := p.lock(ctx); err != nil {
		return err
	}
	defer p.unlock()
	if p.capabilities != nil && p.capabilities.SettingsFields != nil {
		for field := range settings {
			if !includes(p.capabilities.SettingsFields, field) {
				return invalid("Device does not advertise the " + field + " preference")
			}
		}
	}
	return p.request(ctx, http.MethodPatch, "/api/v1/settings", settings, nil, 200)
}
func (p *pico) UpdateTelemetry(ctx context.Context, settings map[string]any) error {
	if err := validateTelemetry(settings); err != nil {
		return err
	}
	if err := p.lock(ctx); err != nil {
		return err
	}
	defer p.unlock()
	if p.capabilities != nil && !p.capabilities.Supports("telemetry_forwarding") {
		return unsupportedCapability("telemetry forwarding")
	}
	return p.request(ctx, http.MethodPut, "/api/v1/telemetry", settings, nil, 200)
}
