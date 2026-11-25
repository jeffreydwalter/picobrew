package device

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPairStillKeepsCredentialInsideDeviceRelay(t *testing.T) {
	key := strings.Repeat("b", 64)
	reject := false
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/picostill" || r.Method != http.MethodPut || r.Header.Get("Authorization") != "Bearer "+testKey {
			t.Errorf("wrong relay destination or credentials")
		}
		var b struct {
			Key, UID, Host string
			Port           int
		}
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.Key != key || b.UID != "0123456789ab" || b.Host != "still.lan" || b.Port != 8081 {
			t.Errorf("wrong device binding")
		}
		if reject {
			w.WriteHeader(409)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid", "message": key})
			return
		}
		w.WriteHeader(202)
		json.NewEncoder(w).Encode(map[string]any{"checking": true, "key": key})
	}))
	defer h.Close()
	z, err := newPico(h.URL, testKey, true)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := newPico("http://still.lan:8081", key, true)
	if err != nil {
		t.Fatal(err)
	}
	result, err := z.PairPicoStill(t.Context(), peer, "0123456789ab")
	encoded, _ := json.Marshal(result)
	if err != nil || !result.Checking || strings.Contains(string(encoded), key) {
		t.Fatal("invalid public link result")
	}
	reject = true
	_, err = z.PairPicoStill(t.Context(), peer, "0123456789ab")
	if err == nil || strings.Contains(err.Error(), key) {
		t.Fatal("private response escaped")
	}
	_, err = z.PairPicoStill(t.Context(), peer, "unknown")
	if err == nil {
		t.Fatal("accepted unverified device identity")
	}
}

func TestPairStockStillUsesExplicitLANAddressAndNoAccessoryKey(t *testing.T) {
	calls := 0
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 4 ||
			body["protocol"] != "stock" || body["uid"] != "001122aabbcc" || body["port"] != float64(80) ||
			body["host"] != "192.168.1.11" || r.URL.Path != "/api/v1/picostill" || r.Method != http.MethodPut ||
			r.Header.Get("Authorization") != "Bearer "+testKey {
			t.Errorf("unexpected stock pairing request: %v", body)
		}
		w.WriteHeader(202)
		json.NewEncoder(w).Encode(map[string]any{"stock_supported": true, "checking": true})
	}))
	defer h.Close()
	z, err := newPico(h.URL, testKey, true)
	if err != nil {
		t.Fatal(err)
	}
	result, err := z.PairStockPicoStill(t.Context(), "192.168.1.11", "001122AABBCC")
	if err != nil || !result.Checking || !result.StockSupported {
		t.Fatal(result, err)
	}
	for _, bad := range []string{"localhost", "::1", "127.0.0.1", "224.0.0.1", "192.168.1.11:80"} {
		if _, err := z.PairStockPicoStill(t.Context(), bad, "001122aabbcc"); err == nil {
			t.Fatal(bad)
		}
	}
	if calls != 1 {
		t.Fatalf("sent %d requests", calls)
	}
}
