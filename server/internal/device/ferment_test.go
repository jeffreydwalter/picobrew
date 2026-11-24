package device

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

func TestFermentMenuDeviceTransport(t *testing.T) {
	want := FermentMenuReply{Request: 42, Result: "list", More: 1, Entries: []FermentMenuEntry{{Name: "Fermenter", Linked: 1}}}
	requests := 0
	p := testPico(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/v1/ferment" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		switch r.Method {
		case "GET":
			replyJSON(w, 200, map[string]any{"request": 43, "list_request": 42, "action": "unlink", "page": 0, "slot": 0})
		case "POST":
			var got FermentMenuReply
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil || !reflect.DeepEqual(want, got) {
				t.Errorf("reply %+v, error %v", got, err)
			}
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	})
	ctx := context.Background()
	req, err := p.FermentRequest(ctx)
	if err != nil || req.Request != 43 || req.ListRequest != 42 || req.Action != "unlink" || req.Slot == nil || *req.Slot != 0 {
		t.Fatalf("menu selection %+v, error %v", req, err)
	}
	if err := p.ReplyFerment(ctx, want); err != nil || requests != 2 {
		t.Fatalf("reply %v, requests %d", err, requests)
	}
}
