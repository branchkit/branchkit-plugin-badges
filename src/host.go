package main

import (
	"encoding/json"
	"html"
	"sync"
	"time"

	"github.com/branchkit/plugin-sdk-go"
)

const (
	pluginID       = "badges"
	rowsCollection = "plugin.badges.targets"
	hudChannel     = "message"
	// The surface's record name in the platform's `_surface` namespace.
	surfaceGateName = pluginID + ".targets"
)

var rowRoles = map[string]branchkit.FieldDisplay{
	"name":   branchkit.FieldDisplayPrimary,
	"handle": branchkit.FieldDisplayPayload,
	"action": branchkit.FieldDisplayPayload,
	"rank":   branchkit.FieldDisplayPayload,
	"x":      branchkit.FieldDisplayPayload,
	"y":      branchkit.FieldDisplayPayload,
	"w":      branchkit.FieldDisplayPayload,
	"h":      branchkit.FieldDisplayPayload,
}

// platform is everything the plugin asks of the app. The plugin answers it
// with the SDK; tests answer it with a recorder.
type platform interface {
	Targets() (*branchkit.NativeAxTargetsResponse, error)
	Perform(handle, action string) (bool, error)
	ReplaceRows(rows []branchkit.CollectionPutEntry) error
	SurfaceGates() ([]string, error)
	Message(text string)
	HideMessage()
}

// Host holds one set of badges at a time. The platform assigns the code
// words and draws them; the plugin only says where the controls are, and
// presses one when its word is said.
type Host struct {
	p  platform
	mu sync.Mutex
	// open is true from published rows until they drain.
	open    bool
	actions map[string]string // handle -> action
	// window is the badged window's frame as read at show, when the app
	// reports it: a window that moves from there leaves its badges behind.
	window *branchkit.AccessibleBounds
	// gen counts messages, so a message's delayed hide never hides the
	// one that replaced it.
	gen        int
	messageFor time.Duration
}

func newHost(p platform) *Host { return &Host{p: p, messageFor: 3 * time.Second} }

func (h *Host) flashLocked(msg string) {
	h.gen++
	gen := h.gen
	h.p.Message(msg)
	time.AfterFunc(h.messageFor, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.gen == gen {
			h.p.HideMessage()
		}
	})
}

// rowRecords maps targets to rows, keyed by handle. Rank is the reading
// order, which is the order code words are assigned in.
func rowRecords(targets []branchkit.AxTarget) ([]branchkit.CollectionPutEntry, error) {
	out := make([]branchkit.CollectionPutEntry, 0, len(targets))
	for i, t := range targets {
		name := t.Role
		if t.Name != nil && *t.Name != "" {
			name = *t.Name
		}
		payload, err := json.Marshal(map[string]any{
			"name":   name,
			"role":   t.Role,
			"handle": t.ID,
			"action": t.Action,
			"rank":   i,
			"x":      t.Bounds.X,
			"y":      t.Bounds.Y,
			"w":      t.Bounds.Width,
			"h":      t.Bounds.Height,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, branchkit.CollectionPutEntry{ID: t.ID, Payload: payload})
	}
	return out, nil
}

// Show reads the window in front and puts badges on its controls; while
// badges are up it reads the window again.
func (h *Host) Show() error {
	res, err := h.p.Targets()
	h.mu.Lock()
	defer h.mu.Unlock()
	if err != nil {
		h.closeLocked("read_failed")
		branchkit.Logf(pluginID, "show: %v", err)
		h.flashLocked("Could not read the window in front.")
		return nil
	}
	if res == nil || len(res.Targets) == 0 {
		h.closeLocked("nothing_found")
		h.flashLocked("Nothing to badge in the window in front.")
		return nil
	}
	rows, err := rowRecords(res.Targets)
	if err != nil {
		return err
	}
	h.actions = make(map[string]string, len(res.Targets))
	for _, t := range res.Targets {
		h.actions[t.ID] = t.Action
	}
	// Never log names: they are another application's contents.
	branchkit.Logf(pluginID, "show: %d control(s), truncated=%v", len(rows), res.Truncated)
	if err := h.p.ReplaceRows(rows); err != nil {
		// What is published is unknown: the old rows may still be there, or
		// the new ones may have landed after the call gave up. Stay open, so
		// "hide badges", "over" or a press still drains them.
		h.open = true
		return err
	}
	h.open = true
	h.window = res.Window
	h.gen++
	h.p.HideMessage()
	return nil
}

// Press acts on the control, then takes the badges down: one press per
// "show badges", so the next one reads the window as it is then.
func (h *Host) Press(handle, action string) error {
	h.mu.Lock()
	if action == "" {
		action = h.actions[handle]
	}
	h.mu.Unlock()
	if action == "" {
		action = "press"
	}
	ok, err := h.p.Perform(handle, action)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closeLocked("pressed")
	if err != nil || !ok {
		branchkit.Logf(pluginID, "press %s: ok=%v err=%v", action, ok, err)
		h.flashLocked(`That control did not respond. Say "show badges" to read the window again.`)
		return err
	}
	return nil
}

// Hide takes the badges down without acting.
func (h *Host) Hide() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closeLocked("hidden")
}

// closeLocked drains the rows: the platform then takes the badges down
// and clears the surface's code words.
func (h *Host) closeLocked(reason string) {
	if !h.open {
		return
	}
	h.open, h.actions, h.window = false, nil, nil
	if err := h.p.ReplaceRows(nil); err != nil {
		branchkit.Logf(pluginID, "drain (%s): %v", reason, err)
	}
	branchkit.Logf(pluginID, "closed: %s", reason)
}

// OnSurfaceChanged drains the rows when something else ended the surface
// ("over", or another list opening): its code words no longer answer, so
// rows kept would only reopen it later.
func (h *Host) OnSurfaceChanged() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.open {
		return
	}
	names, err := h.p.SurfaceGates()
	if err != nil {
		branchkit.Logf(pluginID, "surface read: %v", err)
		return
	}
	for _, n := range names {
		if n == surfaceGateName {
			return
		}
	}
	h.closeLocked("surface_cleared")
}

// OnWindowFocused takes the badges down when another window comes to the
// front: they mark places in the window that was read, and their code words
// would press controls in a window no longer in front.
func (h *Host) OnWindowFocused() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closeLocked("focus_moved")
}

// OnWindowFrameChanged takes the badges down when the badged window moves
// or resizes, which leaves every badge off its control. The event names the
// window by an id the controls read does not carry, so the window is known
// by the frame it had: the one read at show.
func (h *Host) OnWindowFrameChanged(old branchkit.Frame) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.open || h.window == nil {
		return
	}
	near := func(a, b int) bool { return a-b <= 1 && b-a <= 1 }
	w := h.window
	if near(old.X, w.X) && near(old.Y, w.Y) && near(old.W, w.Width) && near(old.H, w.Height) {
		h.closeLocked("window_moved")
	}
}

func renderMessage(msg string) string {
	return `<div class="badges-message">` + html.EscapeString(msg) + `</div>`
}
