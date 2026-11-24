package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jeffreydwalter/picobrew-pico-pro-firmware/server/internal/device"
)

type telemetryTarget struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
}

func (t telemetryTarget) settings() map[string]any {
	return map[string]any{"enabled": t.Enabled, "host": t.Host, "port": t.Port}
}

func (t telemetryTarget) sameReceiver(other telemetryTarget) bool {
	return t.Port == other.Port && strings.EqualFold(strings.TrimSuffix(t.Host, "."), strings.TrimSuffix(other.Host, "."))
}

func (t telemetryTarget) sameConfig(other telemetryTarget) bool {
	return t.Enabled == other.Enabled && t.sameReceiver(other)
}

func parseReceiverURL(value string) (telemetryTarget, error) {
	u, err := url.Parse(value)
	if err != nil || u == nil || u.Scheme != "http" || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.Path != "" && u.Path != "/" {
		return telemetryTarget{}, errors.New("use an HTTP origin with a hostname and optional port, without a path or credentials")
	}
	t := telemetryTarget{Enabled: true, Host: strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")), Port: 80}
	if u.Port() != "" {
		t.Port, err = strconv.Atoi(u.Port())
		if err != nil {
			return telemetryTarget{}, errors.New("invalid receiver port")
		}
	}
	if device.ValidateTelemetry(t.settings()) != nil || t.Host == "localhost" || strings.HasSuffix(t.Host, ".localhost") {
		return telemetryTarget{}, errors.New("the receiver must use a reachable hostname or IPv4 address")
	}
	if ip := net.ParseIP(t.Host); ip != nil && !receiverIPAllowed(ip) {
		return telemetryTarget{}, errors.New("the receiver cannot use loopback, link-local, multicast, or an unspecified address")
	}
	return t, nil
}

func receiverIPAllowed(ip net.IP) bool {
	return ip.To4() != nil && ip.IsGlobalUnicast() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.Equal(net.ParseIP("100.100.100.200"))
}

func newReceiverClient() *http.Client {
	dialer := &net.Dialer{Timeout: 1500 * time.Millisecond}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("invalid receiver address")
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, errors.New("receiver hostname could not be resolved")
		}
		var ipv4 []net.IP
		for _, item := range ips {
			// Firmware uses IPv4. Reject forbidden DNS answers rather than
			// allowing a hostname to turn into a loopback/metadata request.
			if item.IP.IsLoopback() || item.IP.IsUnspecified() || item.IP.IsLinkLocalUnicast() || item.IP.IsMulticast() {
				return nil, errors.New("receiver resolved to a prohibited address")
			}
			if item.IP.To4() != nil {
				if !receiverIPAllowed(item.IP) {
					return nil, errors.New("receiver resolved to a prohibited address")
				}
				ipv4 = append(ipv4, item.IP)
			}
		}
		for _, ip := range ipv4 {
			conn, err := dialer.DialContext(ctx, "tcp4", net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			if ctx.Err() != nil {
				break
			}
		}
		return nil, errors.New("receiver could not be reached")
	}
	return &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (s *Server) receiverProof(challenge string) string {
	mac := hmac.New(sha256.New, []byte(s.telemetryProbeKey))
	_, _ = mac.Write([]byte("picobrew-telemetry-receiver:" + challenge))
	return hex.EncodeToString(mac.Sum(nil))
}

// A bounded, read-only proof that an inferred browser/NAS address reaches this
// Hub instance. No session, device key, or signing key is returned. Forwarded
// headers are never consulted when choosing a callback address.
func (s *Server) telemetryReceiverProbe(w http.ResponseWriter, r *http.Request) {
	challenge := r.URL.Query().Get("challenge")
	decoded, err := hex.DecodeString(challenge)
	if err != nil || len(decoded) != 16 || len(challenge) != 32 || s.telemetryProbeKey == "" {
		s.fail(w, 400, "challenge", "Invalid receiver challenge")
		return
	}
	s.json(w, 200, map[string]string{"proof": s.receiverProof(challenge)})
}

func (s *Server) probeReceiver(ctx context.Context, target telemetryTarget) bool {
	if s.telemetryClient == nil || s.telemetryProbeKey == "" {
		return false
	}
	challenge := randomID()
	u := url.URL{Scheme: "http", Host: net.JoinHostPort(target.Host, strconv.Itoa(target.Port)), Path: "/api/v1/telemetry/receiver", RawQuery: "challenge=" + challenge}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false
	}
	resp, err := s.telemetryClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 257))
	var reply struct {
		Proof string `json:"proof"`
	}
	if err != nil || len(data) > 256 || json.Unmarshal(data, &reply) != nil {
		return false
	}
	return hmac.Equal([]byte(reply.Proof), []byte(s.receiverProof(challenge)))
}

func requestReceiverOrigin(r *http.Request) string {
	if r.TLS != nil {
		return ""
	} // Firmware's forwarding transport is HTTP.
	if _, err := parseReceiverURL("http://" + r.Host); err != nil {
		return ""
	}
	return "http://" + r.Host
}

func routeReceiver(ctx context.Context, baseURL string, port int) (telemetryTarget, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" {
		return telemetryTarget{}, errors.New("invalid machine address")
	}
	// UDP connect selects the outbound interface without transmitting a packet.
	dialer := &net.Dialer{Timeout: time.Second}
	conn, err := dialer.DialContext(ctx, "udp4", net.JoinHostPort(u.Hostname(), "80"))
	if err != nil {
		return telemetryTarget{}, err
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || !receiverIPAllowed(addr.IP) {
		return telemetryTarget{}, errors.New("no reachable local IPv4 route")
	}
	return telemetryTarget{Enabled: true, Host: addr.IP.String(), Port: port}, nil
}

func (s *Server) automaticReceiver(ctx context.Context, baseURL, origin string) (telemetryTarget, string) {
	return s.findReceiver(ctx, baseURL, origin, nil)
}

// During recovery, bypass the address the accessory has already tried.
func (s *Server) findReceiver(ctx context.Context, baseURL, origin string, failed *telemetryTarget) (telemetryTarget, string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if s.cfg.DeviceURL != "" {
		target, err := parseReceiverURL(s.cfg.DeviceURL)
		if err == nil && (failed == nil || !target.sameReceiver(*failed)) && s.probeReceiver(ctx, target) {
			return target, ""
		}
		if failed == nil {
			return telemetryTarget{}, "The configured Hub device URL does not reach this Hub's HTTP receiver. Live readings and history remain available."
		}
	}
	var candidates []telemetryTarget
	if target, err := parseReceiverURL(origin); err == nil {
		candidates = append(candidates, target)
	}
	host, textPort, err := net.SplitHostPort(s.cfg.ListenAddress)
	port, portErr := strconv.Atoi(textPort)
	ip := net.ParseIP(strings.Trim(host, "[]"))
	loopback := host == "localhost" || ip != nil && ip.IsLoopback()
	if err == nil && portErr == nil && port > 0 && port <= 65535 && !loopback {
		if target, err := parseReceiverURL("http://" + net.JoinHostPort(host, textPort)); err == nil {
			candidates = append(candidates, target)
		}
		if ip == nil || ip.IsUnspecified() || ip.To4() != nil {
			if target, err := routeReceiver(ctx, baseURL, port); err == nil {
				candidates = append(candidates, target)
			}
		}
	}
	seen := map[string]bool{}
	for _, target := range candidates {
		key := net.JoinHostPort(target.Host, strconv.Itoa(target.Port))
		if seen[key] || (failed != nil && target.sameReceiver(*failed)) {
			continue
		}
		seen[key] = true
		if s.probeReceiver(ctx, target) {
			return target, ""
		}
	}
	if loopback {
		return telemetryTarget{}, "This Hub is listening only on localhost, so the machine cannot send to it. Restart with a LAN listen address; live readings and history remain available."
	}
	return telemetryTarget{}, "The Hub's reachable HTTP receiver could not be verified. Open the Hub using its LAN address and reconnect; advanced proxy setups can set HUB_DEVICE_URL. Live readings and history remain available."
}
