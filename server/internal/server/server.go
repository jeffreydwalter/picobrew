package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/ai"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/firmware"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/privatefs"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/store"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/usbservice"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/webui"
)

// Version is set to the immutable release tag by the distribution workflow.
var Version = "1.0.0"

type Config struct {
	DataDir, WebDir, Password string
	ListenAddress, DeviceURL  string
	Demo, SecureCookies       bool
	PollInterval              time.Duration
	Logger                    *slog.Logger
}
type runtimeDevice struct {
	updatingFirmware bool // guarded by mu

	mu                 sync.RWMutex
	statusMu           sync.Mutex
	view               domain.DeviceView
	adapter            device.Adapter
	cancel             context.CancelFunc
	commandMu          sync.Mutex
	historyMu          sync.Mutex
	lastHistory        time.Time
	identityMismatch   bool
	awaitingIdentity   bool     // No authenticated application identity observed since registration was loaded.
	keyFingerprint     [32]byte // Private, immutable, and never persisted or returned.
	hasKeyFingerprint  bool
	telemetryDelivery  deliveryWindow // guarded by commandMu
	telemetryNext      time.Time
	telemetryAttempts  int
	telemetryReconnect bool // Guarded by mu; set by a verified reconnect or uptime rollback.
}
type Server struct {
	discovery                 lanDiscoveryState
	pairings                  map[string]*machinePairing // guarded by deviceMu
	newPairingClient          func(string) (registrationPairer, error)
	newDeviceAdapter          func(string, string) (device.Adapter, error)
	cfg                       Config
	db                        *store.Store
	usb                       *usbservice.Manager
	vault                     *vault
	auth                      authState
	mu                        sync.RWMutex
	devices                   map[string]*runtimeDevice
	ctx                       context.Context
	cancel                    context.CancelFunc
	wg                        sync.WaitGroup
	router                    http.Handler
	stockMu                   sync.Mutex
	recipeMu                  sync.Mutex
	aiMu                      sync.Mutex
	configMu                  sync.Mutex
	deviceMu                  sync.Mutex
	aiClient                  *ai.Client
	subscriptionMu            sync.Mutex
	chatGPT                   *ai.ChatGPTClient
	chatGPTCallback           *http.Server
	chatGPTCallbackTimer      *time.Timer
	chatGPTCallbackGeneration uint64
	telemetryProbeKey         string
	telemetryClient           *http.Client
	firmwareGitHub            *firmware.GitHub
	firmwareMu                sync.Mutex
	firmwareWorkMu            sync.Mutex
	releaseMu                 sync.Mutex
}

func New(cfg Config) (*Server, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "data"
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 3 * time.Second
	}
	if cfg.DeviceURL != "" {
		if _, err := parseReceiverURL(cfg.DeviceURL); err != nil {
			return nil, fmt.Errorf("invalid Hub device URL: %w", err)
		}
	}
	if cfg.WebDir == "" {
		if _, err := webui.Files(); err != nil {
			return nil, fmt.Errorf("initialize bundled website: %w", err)
		}
	}
	if err := privatefs.EnsureDir(cfg.DataDir); err != nil {
		return nil, err
	}
	v, e := openVault(cfg.DataDir)
	if e != nil {
		return nil, e
	}
	db, e := store.Open(filepath.Join(cfg.DataDir, "hub.sqlite"))
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{cfg: cfg, db: db, vault: v, devices: map[string]*runtimeDevice{}, ctx: ctx, cancel: cancel, aiClient: ai.New(), chatGPT: ai.NewChatGPT()}
	s.newDeviceAdapter = device.NewPico
	s.usb, e = usbservice.Open(cfg.DataDir, db, !cfg.Demo)
	if e != nil {
		db.Close()
		cancel()
		return nil, e
	}
	s.telemetryProbeKey = secretToken()
	s.telemetryClient = newReceiverClient()
	s.firmwareGitHub = firmware.NewGitHub()
	if e = s.initAuth(); e != nil {
		db.Close()
		cancel()
		return nil, e
	}
	if e = s.recoverDeployments(); e != nil {
		db.Close()
		cancel()
		return nil, e
	}
	if e = s.recoverAIMonitor(); e != nil {
		db.Close()
		cancel()
		return nil, e
	}
	if cfg.Demo {
		if e = s.seedDemo(); e != nil {
			db.Close()
			cancel()
			return nil, e
		}
	}
	rows, e := db.List("device")
	if e != nil {
		db.Close()
		cancel()
		return nil, e
	}
	var loaded []domain.Device
	for _, raw := range rows {
		var d domain.Device
		if e = json.Unmarshal(raw, &d); e != nil {
			db.Close()
			cancel()
			return nil, fmt.Errorf("read stored machine: %w", e)
		}
		if cfg.Demo && d.Adapter != "simulator" {
			db.Close()
			cancel()
			return nil, errors.New("demo mode requires its own data directory containing only simulated machines")
		}
		loaded = append(loaded, d)
	}
	for _, d := range loaded {
		s.attach(d, nil)
	}
	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.monitorAI(ctx) }()
	if !cfg.Demo {
		s.wg.Add(1)
		go func() { defer s.wg.Done(); s.monitorReleases(ctx) }()
	}
	s.resumeChatGPTCallback()
	s.router = s.routes()
	return s, nil
}

// BeginShutdown stops monitoring and live event streams while leaving the store
// open for accepted HTTP requests to finish. Register it with net/http's
// RegisterOnShutdown, then call Close after HTTP shutdown has drained handlers.
func (s *Server) BeginShutdown() {
	s.cancel()
}

func (s *Server) Close() error {
	s.BeginShutdown()
	s.stopChatGPTCallback()
	s.usb.Close()
	s.mu.Lock()
	for _, d := range s.devices {
		d.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
	s.firmwareGitHub.Close()
	return s.db.Close()
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.router.ServeHTTP(w, r) }
func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.cfg.Demo {
				host := r.Host
				if h, _, err := net.SplitHostPort(host); err == nil {
					host = h
				}
				host = strings.Trim(host, "[]")
				ip := net.ParseIP(host)
				if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
					s.fail(w, 403, "demo_host", "Demo mode accepts localhost requests only")
					return
				}
			}
			started := time.Now()
			wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(wrapped, r)
			s.cfg.Logger.Debug("HTTP request", "request_id", middleware.GetReqID(r.Context()), "method", r.Method, "path", r.URL.Path, "status", wrapped.Status(), "duration", time.Since(started))
		})
	})
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "same-origin")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https://img.youtube.com https://i.ytimg.com; connect-src 'self'; worker-src 'self'; frame-src 'none'; object-src 'none'; frame-ancestors 'none'")
			if strings.HasPrefix(r.URL.Path, "/api/") {
				w.Header().Set("Cache-Control", "no-store")
			}
			next.ServeHTTP(w, r)
		})
	})
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		s.json(w, 200, map[string]any{"status": "ok", "version": Version})
	})
	r.Get("/API/*", s.stockWire)
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/auth/session", s.authStatus)
		r.Post("/auth/setup", s.login)
		r.Post("/auth/login", s.login)
		r.Post("/telemetry", s.ingestTelemetry)
		r.Get("/telemetry/receiver", s.telemetryReceiverProbe)
		r.Group(func(r chi.Router) {
			r.Use(s.authenticate)
			r.Post("/auth/logout", s.logout)
			r.Get("/usb/devices", s.usbDevices)
			r.Post("/usb/jobs", s.usbStart)
			r.Get("/usb/jobs/{id}", s.usbJob)
			r.Post("/usb/jobs/{id}/confirm", s.usbAnswer)
			r.Post("/usb/jobs/{id}/cancel", s.usbAnswer)
			r.Get("/devices", s.listDevices)
			r.Get("/devices/discover", s.discoverLAN)
			r.Post("/devices", s.saveDevice)
			r.Post("/devices/pair", s.startMachinePairing)
			r.Post("/devices/{id}/key", s.machineKey)
			r.Post("/devices/{id}/website", s.machineWebsite)
			r.Get("/devices/{id}", s.getDevice)
			r.Get("/devices/{id}/picoferms", s.brewerPicoFerms)
			r.Put("/devices/{id}/picoferms", s.brewerPicoFerms)
			r.Get("/devices/{id}/brewer", s.picoFermBrewer)
			r.Put("/devices/{id}/brewer", s.picoFermBrewer)
			r.Put("/devices/{id}", s.saveDevice)
			r.Delete("/devices/{id}", s.deleteDevice)
			r.Post("/devices/{id}/test", s.testDevice)
			r.Get("/devices/{id}/display", s.display)
			r.Get("/devices/{id}/events", s.deviceEvents)
			r.Post("/devices/{id}/commands", s.command)
			r.Get("/devices/{id}/recipes", s.machineRecipes)
			r.Put("/devices/{id}/recipes/{recipe_id}", s.updateMachineRecipe)
			r.Delete("/devices/{id}/recipes/{recipe_id}", s.deleteMachineRecipe)
			r.Get("/devices/{id}/settings", s.machineSettings)
			r.Patch("/devices/{id}/settings", s.machineSettings)
			r.Get("/devices/{id}/telemetry", s.machineTelemetry)
			r.Put("/devices/{id}/telemetry", s.machineTelemetry)
			r.Post("/devices/{id}/firmware/install", s.installFirmware)
			r.Post("/devices/{id}/firmware/restore-stock", s.restoreStock)
			r.Post("/devices/{id}/firmware/stock-started", s.observeStockStartup)
			r.Get("/devices/{id}/firmware", s.machineFirmware)
			r.Put("/devices/{id}/firmware/hardware", s.verifyFirmwareHardware)
			r.Post("/devices/{id}/sync-history", s.syncHistory)
			r.Post("/devices/{id}/deploy", s.deploy)
			r.Post("/devices/{id}/deployments", s.planDeployment)
			r.Get("/devices/{id}/deployments/{deployment_id}", s.getDeployment)
			r.Post("/devices/{id}/deployments/{deployment_id}/execute", s.executeDeployment)
			r.Post("/stock-zymatics/preview", s.stockOfflinePreview)
			r.Get("/stock-zymatics/{machine}/recipes", s.stockMenu)
			r.Post("/stock-zymatics/{machine}/recipes", s.stockMenu)
			r.Delete("/stock-zymatics/{machine}/recipes/{recipe_id}", s.stockMenu)
			r.Get("/profiles", s.profiles)
			r.Put("/profiles/{id}", s.saveProfile)
			r.Delete("/profiles/{id}", s.saveProfile)
			r.Get("/catalog", s.listCatalog)
			r.Get("/catalog/references", s.catalogReferences)
			r.Get("/catalog/assets/*", s.catalogAsset)
			r.Get("/catalog/{id}", s.getCatalogEntry)
			r.Post("/catalog/{id}/copy", s.copyCatalogEntry)
			r.Get("/catalog/{id}/source", s.catalogOriginal)
			r.Get("/recipes", s.listRecipes)
			r.Post("/recipes", s.saveRecipe)
			r.Post("/recipes/import", s.importRecipes)
			r.Get("/recipes/{id}", s.getRecipe)
			r.Put("/recipes/{id}", s.saveRecipe)
			r.Delete("/recipes/{id}", s.deleteRecipe)
			r.Post("/recipes/{id}/adapt", s.adaptRecipe)
			r.Get("/recipes/{id}/export", s.exportRecipe)
			r.Get("/recipes/{id}/revisions", s.recipeRevisions)
			r.Get("/brews", s.listBrews)
			r.Get("/brews/{id}", s.getBrew)
			r.Patch("/brews/{id}", s.updateBrew)
			r.Post("/brews/{id}/conditioning", s.updateConditioning)
			r.Get("/brews/{id}/export", s.exportBrew)
			r.Get("/events", s.events)
			r.Get("/ai/subscription", s.aiSubscription)
			r.Delete("/ai/subscription", s.aiSubscription)
			r.Post("/ai/subscription/login", s.aiSubscriptionLogin)
			r.Post("/ai/subscription/callback", s.aiSubscriptionCallback)
			r.Get("/ai/settings", s.aiSettings)
			r.Put("/ai/settings", s.aiSettings)
			r.Get("/firmware/updates", s.firmwareUpdates)
			r.Post("/firmware/updates", s.firmwareUpdates)
			r.Post("/firmware/releases/{packageID}/stage", s.stageRelease)
			r.Get("/firmware/settings", s.firmwareSettings)
			r.Put("/firmware/settings", s.firmwareSettings)
			r.Get("/firmware/github", s.firmwareGitHubCatalog)
			r.Post("/firmware/github/import", s.firmwareGitHubImport)
			r.Get("/firmware/packages", s.firmwarePackages)
			r.Post("/firmware/packages", s.firmwarePackages)
			r.Get("/firmware/packages/{packageID}", s.firmwarePackage)
			r.Delete("/firmware/packages/{packageID}", s.firmwarePackage)
			r.Get("/firmware/packages/{packageID}/download", s.downloadFirmwarePackage)
			r.Get("/ai/models", s.aiModels)
			r.Post("/ai/models", s.aiModels)
			r.Post("/ai/assist", s.aiAssist)
			r.Get("/ai/advice", s.aiAdvice)
			r.Get("/devices/{id}/ai-monitor", s.aiMonitor)
			r.Put("/devices/{id}/ai-monitor", s.aiMonitor)
			r.Get("/openapi.json", s.openapi)
			r.Get("/backup", s.backup)
			r.Get("/shopping-list", s.shoppingList)
		})
	})
	r.Get("/*", s.static)
	r.Head("/*", s.static)
	return r
}
func (s *Server) json(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.cfg.Logger.Debug("Response ended", "error", err)
	}
}
func (s *Server) fail(w http.ResponseWriter, status int, code, message string) {
	s.json(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func (s *Server) internal(w http.ResponseWriter, e error) {
	s.cfg.Logger.Error("Request failed", "error", e)
	s.fail(w, 500, "internal_error", "The operation could not be completed")
}
func (s *Server) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		s.fail(w, 415, "content_type", "Use application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(dst); e != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(e, &tooLarge) {
			s.fail(w, 413, "body_too_large", "Request body exceeds 2 MiB")
			return false
		}
		s.fail(w, 400, "invalid_json", "Invalid JSON request")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		s.fail(w, 400, "invalid_json", "Send one JSON value")
		return false
	}
	return true
}
func (s *Server) storeError(w http.ResponseWriter, e error) {
	if errors.Is(e, store.ErrNotFound) {
		s.fail(w, 404, "not_found", "Record not found")
	} else if errors.Is(e, store.ErrConflict) {
		s.fail(w, 409, "conflict", "This record changed; reload before saving")
	} else {
		s.internal(w, e)
	}
}
func (s *Server) deviceError(w http.ResponseWriter, e error) {
	if errors.Is(e, context.Canceled) {
		s.fail(w, 499, "cancelled", "The request was canceled")
		return
	}
	var remote *device.Error
	if errors.As(e, &remote) && remote.UnknownOutcome {
		s.fail(w, 502, "device_outcome_unknown", safeMessage(remote.Message)+". The change may have reached the machine; inspect its current state before submitting it again.")
		return
	}
	if errors.As(e, &remote) && !remote.UnknownOutcome {
		if remote.StatusCode == http.StatusServiceUnavailable && remote.Code == "display_busy" {
			w.Header().Set("Retry-After", "2")
			s.fail(w, remote.StatusCode, "device_display_busy", safeMessage(remote.Message))
			return
		}
		switch remote.StatusCode {
		case 400, 404, 409, 413, 422:
			s.fail(w, remote.StatusCode, "device_"+remote.Code, safeMessage(remote.Message))
			return
		}
	}
	s.cfg.Logger.Debug("Device operation failed", "error", e)
	s.fail(w, 502, "device_error", safeMessage(e.Error()))
}
func safeMessage(v string) string {
	if len(v) > 220 {
		return v[:220]
	}
	return v
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		if !s.authenticated(r) {
			return
		}
		p, _ := json.Marshal(s.fleet())
		if _, e := fmt.Fprintf(w, "event: fleet\ndata: %s\n\n", p); e != nil {
			return
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
