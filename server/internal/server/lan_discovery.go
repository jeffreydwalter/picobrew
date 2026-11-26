package server

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/domain"
)

type DiscoveryCandidate struct {
	Name            string `json:"name"`
	Model           string `json:"model"`
	Source          string `json:"source"`
	BaseURL         string `json:"base_url,omitempty"`
	FirmwareVersion string `json:"firmware_version,omitempty"`
	Serial          string `json:"serial,omitempty"`
}

type LANDiscovery struct {
	Candidates []DiscoveryCandidate `json:"candidates"`
	Partial    bool                 `json:"partial"`
}

type discoveryRun struct {
	done     chan struct{}
	result   LANDiscovery
	err      error
	finished time.Time
}

type lanDiscoveryState struct {
	mu      sync.Mutex
	current *discoveryRun
	legacy  map[string]time.Time
	picos   map[string]time.Time
	attempt uint32
}

// A Zymatic calling the original recipe service identifies itself by serial.
// Proxies are common in that setup, so its HTTP peer is not a device address.
func (s *Server) rememberZymatic(serial string) {
	d := &s.discovery
	d.mu.Lock()
	defer d.mu.Unlock()
	rememberStockCaller(&d.legacy, serial)
}

func (s *Server) rememberStockPico(uid string) {
	s.discovery.mu.Lock()
	defer s.discovery.mu.Unlock()
	rememberStockCaller(&s.discovery.picos, uid)
}

func rememberStockCaller(callers *map[string]time.Time, id string) {
	if *callers == nil {
		*callers = make(map[string]time.Time)
	}
	now := time.Now()
	for key, seen := range *callers {
		if now.Sub(seen) > 15*time.Minute {
			delete(*callers, key)
		}
	}
	if _, known := (*callers)[id]; known || len(*callers) < 64 {
		(*callers)[id] = now
	}
}

func (s *Server) discoverLAN(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery != "" {
		s.fail(w, 400, "discovery", "Discovery uses the Hub's local network interfaces and accepts no parameters.")
		return
	}
	if s.cfg.Demo {
		s.json(w, 200, LANDiscovery{Candidates: []DiscoveryCandidate{}})
		return
	}
	d := &s.discovery
	d.mu.Lock()
	run := d.current
	if run == nil || (!run.finished.IsZero() && time.Since(run.finished) >= 2*time.Second) {
		run = &discoveryRun{done: make(chan struct{})}
		d.current = run
		attempt := d.attempt
		d.attempt++
		go func() {
			ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
			defer cancel()
			run.result, run.err = findLANDevices(ctx, attempt)
			d.mu.Lock()
			run.finished = time.Now()
			close(run.done)
			d.mu.Unlock()
		}()
	}
	d.mu.Unlock()
	select {
	case <-r.Context().Done():
		return
	case <-run.done:
	}
	if run.err != nil {
		s.fail(w, 503, "discovery", "The Hub could not read its local network interfaces.")
		return
	}
	registered := map[string]bool{}
	for _, dev := range s.fleet() {
		registered[discoveryAddressKey(dev.BaseURL)] = true
	}
	stockZ, err := listRecords[stockZProgramMachine](s, stockZProgramKind)
	if err != nil {
		s.storeError(w, err)
		return
	}
	stockAddresses := map[string]bool{}
	for _, machine := range stockZ {
		stockAddresses[discoveryAddressKey(machine.BaseURL)] = true
	}
	result := LANDiscovery{Partial: run.result.Partial, Candidates: []DiscoveryCandidate{}}
	for _, item := range run.result.Candidates {
		// A former stock Z flashed to standalone still needs pairing.
		address := discoveryAddressKey(item.BaseURL)
		knownZ := item.Source == "stock" && item.Model == "z-series" && stockAddresses[address]
		if !knownZ && !registered[address] {
			result.Candidates = append(result.Candidates, item)
		}
	}
	picos, err := listRecords[stockPicoBrewer](s, stockPicoKind)
	if err != nil {
		s.storeError(w, err)
		return
	}
	knownPicos := map[string]bool{}
	for _, p := range picos {
		knownPicos[p.UID] = true
	}
	d.mu.Lock()
	for serial, seen := range d.legacy {
		if time.Since(seen) <= 15*time.Minute {
			result.Candidates = append(result.Candidates, DiscoveryCandidate{Name: "Zymatic", Model: "zymatic", Source: "zymatic-sync", Serial: serial})
		}
	}
	for uid, seen := range d.picos {
		if !knownPicos[uid] && time.Since(seen) <= 15*time.Minute {
			result.Candidates = append(result.Candidates, DiscoveryCandidate{Name: "Stock Pico", Source: "pico-register", Serial: uid})
		}
	}
	d.mu.Unlock()
	sort.Slice(result.Candidates, func(i, j int) bool {
		a, b := result.Candidates[i], result.Candidates[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.BaseURL != b.BaseURL {
			return a.BaseURL < b.BaseURL
		}
		return a.Serial < b.Serial
	})
	s.json(w, 200, result)
}

func discoveryAddressKey(address string) string {
	u, err := url.Parse(address)
	if err == nil && u.Scheme == "http" && (u.Port() == "" || u.Port() == "80") {
		return strings.ToLower(u.Hostname())
	}
	return address
}

type lanInterface struct {
	local   netip.Addr
	network netip.Prefix
}

// Use only private IPv4 ranges directly attached to broadcast-capable
// interfaces. The first neighborhood of each search contains the Hub. On
// wider subnets also rotate through an adjacent /24 on subsequent searches.
func discoveryNetworks(attempt uint32) ([]lanInterface, bool, error) {
	networks, partial, err := attachedLANs()
	if len(networks) > 4 {
		// Always include the primary interface and rotate the other three.
		selected := []lanInterface{networks[0]}
		for i := 0; i < 3; i++ {
			selected = append(selected, networks[1+(int(attempt%uint32(len(networks)-1))+i)%(len(networks)-1)])
		}
		networks, partial = selected, true
	}
	return networks, partial, err
}

func attachedLANs() ([]lanInterface, bool, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, false, err
	}
	var networks []lanInterface
	partial := false
	for _, iface := range interfaces {
		if iface.Flags&(net.FlagUp|net.FlagBroadcast) != net.FlagUp|net.FlagBroadcast || iface.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			partial = true
			continue
		}
		for _, addr := range addrs {
			prefix, err := netip.ParsePrefix(addr.String())
			if err != nil || !prefix.Addr().Is4() || !prefix.Addr().IsPrivate() {
				continue
			}
			local := prefix.Addr()
			network := lanInterface{local: local, network: prefix.Masked()}
			if !slices.Contains(networks, network) {
				networks = append(networks, network)
			}
		}
	}
	return networks, partial || len(networks) == 0, nil
}

func (n lanInterface) neighborhoods(attempt uint32) []netip.Prefix {
	if n.network.Bits() >= 24 {
		return []netip.Prefix{n.network}
	}
	own := netip.PrefixFrom(n.local, 24).Masked()
	octets := own.Addr().As4()
	base := binary.BigEndian.Uint32(octets[:])
	count := uint32(1) << (24 - n.network.Bits())
	offset := 1 + attempt%(count-1)
	mask := count*256 - 1
	other := (base &^ mask) | ((base + offset*256) & mask)
	binary.BigEndian.PutUint32(octets[:], other)
	return []netip.Prefix{own, netip.PrefixFrom(netip.AddrFrom4(octets), 24)}
}

func (n lanInterface) peers(block netip.Prefix) []netip.Addr {
	var peers []netip.Addr
	first := n.network.Addr()
	for ip := block.Addr(); block.Contains(ip); ip = ip.Next() {
		if !ip.Is4() || !ip.IsPrivate() || ip == n.local || (n.network.Bits() < 31 && (ip == first || !n.network.Contains(ip.Next()))) {
			continue
		}
		peers = append(peers, ip)
	}
	return peers
}

func findLANDevices(ctx context.Context, attempt uint32) (LANDiscovery, error) {
	networks, partial, err := discoveryNetworks(attempt)
	result := LANDiscovery{Partial: partial}
	if err != nil {
		return result, err
	}
	type target struct {
		ip     netip.Addr
		client *http.Client
	}
	var chunks [][]target
	seen := map[netip.Addr]bool{}
	for _, network := range networks {
		seen[network.local] = true
	}
	for _, network := range networks {
		client := newDiscoveryClient(network.local)
		blocks := network.neighborhoods(attempt)
		result.Partial = result.Partial || network.network.Bits() < 23
		for _, block := range blocks {
			var chunk []target
			for _, ip := range network.peers(block) {
				if !seen[ip] {
					seen[ip] = true
					chunk = append(chunk, target{ip: ip, client: client})
				}
			}
			if len(chunk) > 0 {
				chunks = append(chunks, chunk)
			}
		}
	}
	// Interleave interfaces and rotate the starting host so a slower segment
	// cannot monopolize the search or repeatedly starve the same addresses.
	var targets []target
	for i := 0; i < 256; i++ {
		for _, chunk := range chunks {
			if i < len(chunk) {
				targets = append(targets, chunk[(i+int(attempt%256)*37)%len(chunk)])
			}
		}
	}
	queue := make(chan target, len(targets))
	for _, item := range targets {
		queue <- item
	}
	close(queue)
	var workers sync.WaitGroup
	var mu sync.Mutex
	for range min(32, len(targets)) {
		workers.Go(func() {
			for item := range queue {
				if ctx.Err() != nil {
					return
				}
				if candidate, ok := discoverDevice(ctx, item.client, item.ip); ok {
					mu.Lock()
					result.Candidates = append(result.Candidates, candidate)
					mu.Unlock()
				}
			}
		})
	}
	workers.Wait()
	result.Partial = result.Partial || ctx.Err() != nil
	return result, nil
}

func newDiscoveryClient(local netip.Addr) *http.Client {
	dialer := &net.Dialer{LocalAddr: &net.TCPAddr{IP: local.AsSlice()}, Timeout: 500 * time.Millisecond}
	return &http.Client{
		Transport:     &http.Transport{DialContext: dialer.DialContext, DisableKeepAlives: true, MaxResponseHeaderBytes: 8192},
		Timeout:       1500 * time.Millisecond,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func readDiscoveryPage(ctx context.Context, client *http.Client, address, path string) []byte {
	data, _ := discoveryResponse(ctx, client, address, path, 4096, false)
	return data
}

func discoveryResponse(ctx context.Context, client *http.Client, address, path string, limit int, prefix bool) ([]byte, int) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address+path, nil)
	if err != nil {
		return nil, 0
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode
	}
	if !prefix {
		limit++
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)))
	if err != nil || (!prefix && len(data) == limit) {
		return nil, resp.StatusCode
	}
	return data, resp.StatusCode
}

func discoverDevice(ctx context.Context, client *http.Client, ip netip.Addr) (DiscoveryCandidate, bool) {
	address := "http://" + ip.String()
	var health struct {
		domain.Capabilities
		OK   bool   `json:"ok"`
		Mode string `json:"mode"`
	}
	data, status := discoveryResponse(ctx, client, address, "/api/v1/health", 4096, false)
	if status == 0 || (status != 200 && status != 404) {
		return DiscoveryCandidate{}, false
	}
	if json.Unmarshal(data, &health) != nil {
		return recognizeStockDevice(ctx, client, ip)
	}
	model, err := device.ReportedModel(health.Capabilities)
	if err == nil && model != "" && health.ModelFamily != "" && health.APIVersion == 1 && health.Mode == "standalone" && health.FirmwareVersion != "" && len(health.FirmwareVersion) <= 64 {
		return DiscoveryCandidate{Name: discoveryLabel(model), Model: model, FirmwareVersion: health.FirmwareVersion, BaseURL: address, Source: "standalone"}, true
	}
	// Both shipped Zymatic builds have a minimal health reply; identify the
	// public site before treating it as a PicoBrew device.
	if health.OK && model == "" {
		page := readDiscoveryPage(ctx, client, address, "/")
		if bytes.Contains(page, []byte("<h1>PicoBrew Zymatic</h1>")) && (bytes.Contains(page, []byte("<title>Zymatic</title>")) || bytes.Contains(page, []byte("<title>PicoBrew Zymatic</title>"))) {
			return DiscoveryCandidate{Name: "Zymatic", Model: "zymatic", BaseURL: address, Source: "standalone"}, true
		}
	}
	return DiscoveryCandidate{}, false
}

func discoveryLabel(model string) string {
	return map[string]string{"pico-s": "Pico S", "pico-pro": "Pico Pro", "pico-c": "Pico C", "z-series": "Pico Z", "zymatic": "Zymatic", "picoferm": "PicoFerm", "picostill": "PicoStill"}[model]
}
