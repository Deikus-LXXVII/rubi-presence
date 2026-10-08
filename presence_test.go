package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Deikus-LXXVII/rubi/sdk/rubiplugin"
)

type fakeHost struct {
	config  map[string]any
	state   json.RawMessage
	levels  map[string]rubiplugin.Level
	pending []rubiplugin.Request
	events  []map[string]any
	hooks   map[string]string
}

func (h *fakeHost) Config(v any) error {
	cfg := map[string]any{}
	for _, f := range manifest().Config {
		cfg[f.Key] = f.Default
	}
	for k, x := range h.config {
		cfg[k] = x
	}
	b, _ := json.Marshal(cfg)
	return json.Unmarshal(b, v)
}
func (h *fakeHost) HookURL(account, name string, rotate bool) (string, error) {
	if h.hooks == nil {
		h.hooks = map[string]string{}
	}
	if _, ok := h.hooks[name]; !ok || rotate {
		h.hooks[name] = "https://gw.example/h/route/" + randomID()
	}
	return h.hooks[name], nil
}
func (h *fakeHost) LoadState(v any) error {
	if h.state == nil {
		return nil
	}
	return json.Unmarshal(h.state, v)
}
func (h *fakeHost) SaveState(v any) error {
	b, err := json.Marshal(v)
	h.state = b
	return err
}
func (h *fakeHost) Level(kind string) rubiplugin.Level {
	if l, ok := h.levels[kind]; ok {
		return l
	}
	return rubiplugin.None
}
func (h *fakeHost) Submit(_ context.Context, r rubiplugin.Request) (map[string]any, error) {
	h.pending = append(h.pending, r)
	return map[string]any{"status": "awaiting_approval"}, nil
}
func (h *fakeHost) Emit(typ string, data map[string]any) (string, error) {
	return h.EmitTo("", typ, data)
}
func (h *fakeHost) EmitTo(agent, typ string, data map[string]any) (string, error) {
	h.events = append(h.events, map[string]any{"agent": agent, "type": typ, "data": data})
	return "evt", nil
}
func (h *fakeHost) Audit(string, map[string]any) {}
func (h *fakeHost) Logf(string, ...any)          {}

func setup() (*presence, *fakeHost, *time.Time) {
	now := time.Date(2026, 10, 8, 18, 0, 0, 0, time.UTC)
	p := &presence{}
	p.now = func() time.Time { return now }
	return p, &fakeHost{}, &now
}

func TestAddresses(t *testing.T) {
	p, h, _ := setup()
	opts, err := p.addresses(h)
	if err != nil || len(opts) != 4 || opts[0].Label != "Arrive at Home" || opts[3].Label != "Leave Work" {
		t.Fatalf("addresses: %v %v", opts, err)
	}
	again, _ := p.addresses(h)
	if again[0].Key != opts[0].Key {
		t.Fatal("addresses changed between visits")
	}
	if _, ok := h.hooks["home:arrive"]; !ok {
		t.Fatalf("hook names: %v", h.hooks)
	}
}

func TestReportsAndWhere(t *testing.T) {
	p, h, now := setup()
	if out, _ := p.where(h); out.(map[string]any)["known"] != false {
		t.Fatalf("no reports: %v", out)
	}
	must := func(name string) {
		t.Helper()
		if err := p.hook(h, name); err != nil {
			t.Fatal(err)
		}
	}
	must("home:arrive")
	must("home:arrive") // Shortcuts fired twice: one event
	if len(h.events) != 1 || h.events[0]["type"] != "arrived" {
		t.Fatalf("events: %v", h.events)
	}
	out, _ := p.where(h)
	at := out.(map[string]any)["at"].([]map[string]any)
	if len(at) != 1 || at[0]["place"] != "Home" {
		t.Fatalf("where: %v", out)
	}
	*now = now.Add(time.Hour)
	must("home:leave")
	*now = now.Add(time.Hour)
	must("work:arrive")
	must("gym:arrive") // not one of the places: ignored
	out, _ = p.where(h)
	at = out.(map[string]any)["at"].([]map[string]any)
	if len(at) != 1 || at[0]["place"] != "Work" {
		t.Fatalf("at work: %v", out)
	}
	hist, _ := p.history(h, 10)
	if r := hist.(map[string]any)["reports"].([]report); len(r) != 3 || r[0].Place != "Work" {
		t.Fatalf("history: %v", r)
	}

	// Sharing only home: work is hidden everywhere.
	h.config = map[string]any{"share": "home"}
	out, _ = p.where(h)
	if m := out.(map[string]any); m["at_home"] != false || m["left_home_at"] == nil || m["at"] != nil {
		t.Fatalf("home only: %v", m)
	}
	hist, _ = p.history(h, 10)
	for _, r := range hist.(map[string]any)["reports"].([]report) {
		if r.Place != "Home" {
			t.Fatalf("history shows %s", r.Place)
		}
	}
	n := len(h.events)
	*now = now.Add(time.Hour)
	must("work:leave")
	if len(h.events) != n {
		t.Fatalf("an event about Work while sharing only home: %v", h.events[n:])
	}
}

func TestWatches(t *testing.T) {
	p, h, now := setup()
	ctx := context.Background()
	if _, err := p.requestWatch(ctx, h, watchIn{Place: "Moon", Event: "arrive"}); err == nil || !strings.Contains(err.Error(), "Home, Work") {
		t.Fatalf("unknown place: %v", err)
	}
	if _, err := p.requestWatch(ctx, h, watchIn{Place: "home", Event: "arrive", Agent: "Helper", Note: "remind about the parcel"}); err != nil {
		t.Fatal(err)
	}
	keep := false
	if _, err := p.requestWatch(ctx, h, watchIn{Place: "Work", Event: "any", Agent: "Planner", Once: &keep}); err != nil {
		t.Fatal(err)
	}
	_ = p.hook(h, "home:arrive")
	var helper int
	for _, e := range h.events {
		if e["agent"] == "Helper" && e["data"].(map[string]any)["watch"].(map[string]any)["note"] == "remind about the parcel" {
			helper++
		}
	}
	if helper != 1 {
		t.Fatalf("watch didn't fire: %v", h.events)
	}
	*now = now.Add(time.Hour)
	_ = p.hook(h, "home:leave")
	*now = now.Add(5 * time.Minute)
	_ = p.hook(h, "home:arrive")
	st, _ := p.load(h)
	if len(st.Watches) != 1 || st.Watches[0].Agent != "Planner" {
		t.Fatalf("the one-time watch stayed: %+v", st.Watches)
	}
	_ = p.hook(h, "work:arrive")
	_ = p.hook(h, "work:leave")
	st, _ = p.load(h)
	if st.Watches[0].Hits != 2 {
		t.Fatalf("repeating watch hits: %d", st.Watches[0].Hits)
	}
	// Expiry.
	*now = now.Add(40 * 24 * time.Hour)
	_ = p.hook(h, "work:arrive")
	st, _ = p.load(h)
	if len(st.Watches) != 0 {
		t.Fatalf("expired watch kept: %+v", st.Watches)
	}

	// With approval required, nothing is added until the user approves.
	h.levels = map[string]rubiplugin.Level{kindWatch: rubiplugin.Strong, kindRead: rubiplugin.Chat}
	if _, err := p.requestWatch(ctx, h, watchIn{Place: "Home", Event: "leave"}); err != nil || len(h.pending) != 1 {
		t.Fatalf("gated watch: %v %v", err, h.pending)
	}
	if res, _ := p.gated(ctx, h, "where", func() (any, error) { return "leaked", nil }); res == "leaked" {
		t.Fatal("read ran without approval")
	}
}
