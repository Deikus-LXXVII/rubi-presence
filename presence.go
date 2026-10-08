package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Deikus-LXXVII/rubi/sdk/rubiplugin"
)

// host is what the plugin needs from Rubi (*rubiplugin.Host; a fake in tests).
type host interface {
	Config(v any) error
	HookURL(account, name string, rotate bool) (string, error)
	LoadState(v any) error
	SaveState(v any) error
	Level(kind string) rubiplugin.Level
	Submit(ctx context.Context, r rubiplugin.Request) (map[string]any, error)
	Emit(typ string, data map[string]any) (string, error)
	EmitTo(agent, typ string, data map[string]any) (string, error)
	Audit(event string, fields map[string]any)
	Logf(format string, args ...any)
}

type config struct {
	Places []string `json:"places"`
	Share  string   `json:"share"` // "places" | "home"
}

func configOf(h host) config {
	c := config{Places: []string{"Home", "Work"}, Share: "places"}
	if err := h.Config(&c); err != nil {
		h.Logf("settings unavailable: %v", err)
		c.Share = "home" // the more private choice
	}
	var places []string
	for _, p := range c.Places {
		if p = strings.TrimSpace(p); p != "" && len(places) < 20 {
			places = append(places, p)
		}
	}
	c.Places = places
	return c
}

func (c config) home() string {
	if len(c.Places) == 0 {
		return ""
	}
	return c.Places[0]
}

// visible reports whether the agent may know about a place.
func (c config) visible(place string) bool {
	return c.Share != "home" || strings.EqualFold(place, c.home())
}

// place finds a configured place by name, any case.
func (c config) place(name string) (string, bool) {
	for _, p := range c.Places {
		if strings.EqualFold(p, strings.TrimSpace(name)) {
			return p, true
		}
	}
	return "", false
}

// hookName is a hook's name for a place and event; the place is lowercased so renaming "home" to
// "Home" keeps the addresses.
func hookName(place, event string) string { return strings.ToLower(place) + ":" + event }

type report struct {
	Place string    `json:"place"`
	Event string    `json:"event"` // "arrive" | "leave"
	At    time.Time `json:"at"`
}

type watch struct {
	ID      string    `json:"id"`
	Place   string    `json:"place"`
	Event   string    `json:"event"` // "arrive" | "leave" | "any"
	Agent   string    `json:"agent,omitempty"`
	Note    string    `json:"note,omitempty"`
	Once    bool      `json:"once"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
	Hits    int       `json:"hits"`
}

type state struct {
	At      map[string]time.Time `json:"at"` // place -> since when
	Reports []report             `json:"reports"`
	Watches []*watch             `json:"watches,omitempty"`
}

const (
	maxReports = 200
	maxWatches = 30
	// A repeated report within this window is the same event (Shortcuts can fire twice).
	debounce = 3 * time.Minute
)

type presence struct {
	mu  sync.Mutex
	now func() time.Time
}

func (p *presence) clock() time.Time {
	if p.now != nil {
		return p.now()
	}
	return time.Now().UTC()
}

func (p *presence) load(h host) (*state, error) {
	st := &state{At: map[string]time.Time{}}
	if err := h.LoadState(st); err != nil {
		return nil, err
	}
	if st.At == nil {
		st.At = map[string]time.Time{}
	}
	return st, nil
}

func newPlugin(p *presence) *rubiplugin.Plugin {
	pl := rubiplugin.New(manifest())
	pl.ConfigOptions = func(ctx context.Context, h *rubiplugin.Host, _, key string) ([]rubiplugin.Option, error) {
		if key != "addresses" {
			return nil, nil
		}
		return p.addresses(h)
	}
	pl.OnHook = func(ctx context.Context, h *rubiplugin.Host, ev rubiplugin.HookEvent) error {
		return p.hook(h, ev.Name)
	}
	rubiplugin.AddTool(pl, "presence_where", "Where the user is: which of their named places (e.g. Home, Work) they're at, or the last place they left. Never coordinates. Based on their iPhone's reports, which can be late or missed.",
		func(ctx context.Context, h *rubiplugin.Host, _ struct{}) (any, error) {
			return p.gated(ctx, h, "where", func() (any, error) { return p.where(h) })
		})
	rubiplugin.AddTool(pl, "presence_history", "Recent arrivals and departures, newest first.",
		func(ctx context.Context, h *rubiplugin.Host, in struct {
			Limit int `json:"limit,omitempty" jsonschema:"default 20, max 100"`
		}) (any, error) {
			return p.gated(ctx, h, "history", func() (any, error) { return p.history(h, in.Limit) })
		})
	rubiplugin.AddTool(pl, "presence_watch", "Get woken when the user arrives at or leaves a place, e.g. to remind them of something when they get home. Pass your Bot name as agent and a note on what to do.",
		func(ctx context.Context, h *rubiplugin.Host, in watchIn) (any, error) {
			return p.requestWatch(ctx, h, in)
		})
	rubiplugin.AddTool(pl, "presence_watches", "Active location watches.",
		func(ctx context.Context, h *rubiplugin.Host, _ struct{}) (any, error) {
			st, err := p.load(h)
			if err != nil {
				return nil, err
			}
			return map[string]any{"watches": st.active(p.clock())}, nil
		})
	rubiplugin.AddTool(pl, "presence_unwatch", "Remove a location watch.",
		func(ctx context.Context, h *rubiplugin.Host, in struct {
			ID string `json:"id"`
		}) (any, error) {
			return p.unwatch(h, in.ID)
		})
	pl.OnExecute(kindRead, func(ctx context.Context, h *rubiplugin.Host, _ string, payload json.RawMessage) (any, error) {
		var what string
		_ = json.Unmarshal(payload, &what)
		if what == "history" {
			return p.history(h, 20)
		}
		return p.where(h)
	})
	pl.OnExecute(kindWatch, func(ctx context.Context, h *rubiplugin.Host, _ string, payload json.RawMessage) (any, error) {
		var w watch
		if err := json.Unmarshal(payload, &w); err != nil {
			return nil, err
		}
		return p.addWatch(h, &w)
	})
	return pl
}

// addresses lists the hook address of every place and event, for the user to paste into Shortcuts.
func (p *presence) addresses(h host) ([]rubiplugin.Option, error) {
	var out []rubiplugin.Option
	for _, place := range configOf(h).Places {
		for _, ev := range []struct{ name, label string }{{"arrive", "Arrive at "}, {"leave", "Leave "}} {
			u, err := h.HookURL("", hookName(place, ev.name), false)
			if err != nil {
				return nil, err
			}
			out = append(out, rubiplugin.Option{Key: u, Label: ev.label + place})
		}
	}
	return out, nil
}

// gated runs a read right away unless the user asked to approve reading.
func (p *presence) gated(ctx context.Context, h host, what string, run func() (any, error)) (any, error) {
	if h.Level(kindRead) == rubiplugin.None {
		return run()
	}
	return h.Submit(ctx, rubiplugin.Request{Kind: kindRead, Summary: "Let your agent see where you are",
		Preview: map[string]string{"shows": "place names only (" + what + ")"},
		Options: []rubiplugin.Option{{Key: "allow", Label: "Allow"}}, Payload: what})
}

// hook records a report from the user's iPhone.
func (p *presence) hook(h host, name string) error {
	i := strings.LastIndex(name, ":")
	if i < 0 {
		return nil
	}
	cfg := configOf(h)
	event := name[i+1:]
	var place string
	for _, pl := range cfg.Places {
		if strings.ToLower(pl) == name[:i] {
			place = pl
		}
	}
	if place == "" || event != "arrive" && event != "leave" {
		h.Logf("report for a place that's no longer configured") // no place names in a plain-text log
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	st, err := p.load(h)
	if err != nil {
		return err
	}
	now := p.clock()
	if n := len(st.Reports); n > 0 {
		last := st.Reports[n-1]
		if last.Place == place && last.Event == event && now.Sub(last.At) < debounce {
			return nil
		}
	}
	if event == "arrive" {
		if _, already := st.At[place]; !already {
			st.At[place] = now
		}
	} else {
		delete(st.At, place)
	}
	st.Reports = append(st.Reports, report{Place: place, Event: event, At: now})
	if len(st.Reports) > maxReports {
		st.Reports = st.Reports[len(st.Reports)-maxReports:]
	}
	fired := p.matchWatches(h, st, cfg, place, event, now)
	st.prune(now)
	if err := h.SaveState(st); err != nil {
		return err
	}
	h.Audit("reported", map[string]any{"event": event, "watches": fired})
	if cfg.visible(place) {
		typ := map[string]string{"arrive": "arrived", "leave": "left"}[event]
		_, _ = h.Emit(typ, map[string]any{"place": place, "at": now})
	}
	return nil
}

func (p *presence) where(h host) (any, error) {
	cfg := configOf(h)
	st, err := p.load(h)
	if err != nil {
		return nil, err
	}
	if len(st.Reports) == 0 {
		return map[string]any{"known": false, "note": "No reports yet. The user sets up iPhone Shortcuts automations " +
			"with the addresses in the Location plugin's settings in the Rubi panel."}, nil
	}
	if cfg.Share == "home" {
		home := cfg.home()
		if since, ok := st.At[home]; ok {
			return map[string]any{"at_home": true, "since": since}, nil
		}
		out := map[string]any{"at_home": false}
		for i := len(st.Reports) - 1; i >= 0; i-- {
			if r := st.Reports[i]; r.Place == home && r.Event == "leave" {
				out["left_home_at"] = r.At
				break
			}
		}
		out["note"] = "The user shares only whether they're at home."
		return out, nil
	}
	at := []map[string]any{}
	for _, place := range cfg.Places {
		if since, ok := st.At[place]; ok {
			at = append(at, map[string]any{"place": place, "since": since})
		}
	}
	out := map[string]any{"at": at, "places": cfg.Places}
	if len(at) == 0 {
		last := st.Reports[len(st.Reports)-1]
		out["last"] = last
		out["note"] = "Not at any of their places (as far as their iPhone reported)."
	}
	return out, nil
}

func (p *presence) history(h host, limit int) (any, error) {
	cfg := configOf(h)
	st, err := p.load(h)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, 100)
	out := []report{}
	for i := len(st.Reports) - 1; i >= 0 && len(out) < limit; i-- {
		if r := st.Reports[i]; cfg.visible(r.Place) {
			out = append(out, r)
		}
	}
	return map[string]any{"reports": out}, nil
}

// ---- watches ----

type watchIn struct {
	Place string `json:"place" jsonschema:"one of the user's places, e.g. Home"`
	Event string `json:"event" jsonschema:"arrive, leave or any"`
	Agent string `json:"agent,omitempty" jsonschema:"your Bot's name as connected to Rubi, so you are the one woken"`
	Note  string `json:"note,omitempty" jsonschema:"what to do when it happens (sent back to you)"`
	Once  *bool  `json:"once,omitempty" jsonschema:"remove the watch after it fires (default true)"`
	Days  int    `json:"days,omitempty" jsonschema:"how long to watch, default 30, max 180"`
}

func (s *state) active(now time.Time) []*watch {
	out := []*watch{}
	for _, w := range s.Watches {
		if now.Before(w.Expires) {
			out = append(out, w)
		}
	}
	return out
}

func (s *state) prune(now time.Time) { s.Watches = s.active(now) }

func (p *presence) requestWatch(ctx context.Context, h host, in watchIn) (any, error) {
	cfg := configOf(h)
	place, ok := cfg.place(in.Place)
	if !ok || !cfg.visible(place) {
		names := cfg.Places
		if cfg.Share == "home" {
			names = []string{cfg.home()}
		}
		return nil, fmt.Errorf("unknown place %q; the user's places: %s", in.Place, strings.Join(names, ", "))
	}
	event := strings.ToLower(strings.TrimSpace(in.Event))
	if event != "arrive" && event != "leave" && event != "any" {
		return nil, errors.New("event must be arrive, leave or any")
	}
	if len(in.Note) > 1000 {
		return nil, errors.New("keep the note under 1000 characters")
	}
	days := in.Days
	if days <= 0 {
		days = 30
	}
	days = min(days, 180)
	once := in.Once == nil || *in.Once
	now := p.clock()
	w := &watch{ID: randomID(), Place: place, Event: event, Agent: strings.TrimSpace(in.Agent), Note: strings.TrimSpace(in.Note),
		Once: once, Created: now, Expires: now.Add(time.Duration(days) * 24 * time.Hour)}
	if h.Level(kindWatch) == rubiplugin.None {
		return p.addWatch(h, w)
	}
	return h.Submit(ctx, rubiplugin.Request{Kind: kindWatch, Summary: "Tell your agent when you " + event + " " + place,
		Preview: map[string]any{"place": place, "event": event, "wakes": w.Agent, "until": w.Expires.Format("2006-01-02")},
		Options: []rubiplugin.Option{{Key: "allow", Label: "Allow"}}, Payload: w})
}

func (p *presence) addWatch(h host, w *watch) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, err := p.load(h)
	if err != nil {
		return nil, err
	}
	st.prune(p.clock())
	if len(st.Watches) >= maxWatches {
		return nil, fmt.Errorf("at most %d watches; remove some first", maxWatches)
	}
	st.Watches = append(st.Watches, w)
	if err := h.SaveState(st); err != nil {
		return nil, err
	}
	h.Audit("watch_started", map[string]any{"watch_id": w.ID})
	out := map[string]any{"status": "watching", "watch": w}
	if w.Agent == "" {
		out["note"] = "No agent given: it wakes the Bots subscribed to Location events (or the default Bot)."
	}
	return out, nil
}

func (p *presence) unwatch(h host, id string) (any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, err := p.load(h)
	if err != nil {
		return nil, err
	}
	for i, w := range st.Watches {
		if w.ID == id {
			st.Watches = append(st.Watches[:i], st.Watches[i+1:]...)
			return map[string]any{"removed": true}, h.SaveState(st)
		}
	}
	return map[string]any{"removed": false}, nil
}

// matchWatches wakes the agents whose watches match a report; one-time watches are then removed.
func (p *presence) matchWatches(h host, st *state, cfg config, place, event string, now time.Time) int {
	fired := 0
	kept := st.Watches[:0]
	for _, w := range st.Watches {
		match := now.Before(w.Expires) && w.Place == place && (w.Event == "any" || w.Event == event) && cfg.visible(place)
		if match {
			fired++
			w.Hits++
			_, _ = h.EmitTo(w.Agent, map[string]string{"arrive": "arrived", "leave": "left"}[event], map[string]any{
				"place": place, "at": now,
				"watch": map[string]any{"id": w.ID, "note": w.Note, "once": w.Once},
			})
			if w.Once {
				continue
			}
		}
		kept = append(kept, w)
	}
	st.Watches = kept
	return fired
}

func randomID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "loc_" + base64.RawURLEncoding.EncodeToString(b)
}
