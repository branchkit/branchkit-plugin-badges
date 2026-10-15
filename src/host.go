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
	// Front reads which application and window are in front now: the same
	// read as Targets, kept to one control.
	Front() (*branchkit.NativeAxTargetsResponse, error)
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
	pid     int
	// first is the handle of the window's first control as read at show.
	// A handle names one element for as long as the platform keeps it, so
	// reading the front again and finding this handle first means the
	// badged window is still the one in front.
	first string
	// firstAt is where that control was: a window that moves or resizes
	// moves its controls, and leaves its badges behind.
	firstAt branchkit.AccessibleBounds
	// windowAt is the window's own bounds from the same read, when it
	// reports them: a resize can leave the first control where it was.
	windowAt *branchkit.AccessibleBounds
	// moved counts window focus and frame events, open or not, so a show
	// can tell one arrived while it was reading the window.
	moved int
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
	h.mu.Lock()
	movedBefore := h.moved
	h.mu.Unlock()
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
	h.pid, h.first, h.firstAt = res.Pid, res.Targets[0].ID, res.Targets[0].Bounds
	h.windowAt = res.Window
	h.gen++
	h.p.HideMessage()
	// A window was focused, moved or resized while the read ran (it can
	// take seconds), and no later event will come to say so: check once.
	if h.moved != movedBefore && !h.stillFrontLocked(true) {
		h.closeLocked("moved_while_reading")
	}
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
	h.open, h.actions, h.first, h.windowAt = false, nil, "", nil
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
//
// The event comes from a poll and can arrive seconds late, so a switch to a
// window followed at once by "show badges" reports that very window after
// its badges are up. The event's window id is not one the controls read
// carries, so the front is read again (one control) and the badges stay
// only when its first control is the very element read at show: the same
// window. Two windows of one application with the same frame still differ
// here, and any doubt (a failed read, a window whose contents changed)
// takes the badges down.
func (h *Host) OnWindowFocused() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.moved++
	if h.open && !h.stillFrontLocked(false) {
		h.closeLocked("focus_moved")
	}
}

// stillFrontLocked reads the front again and says whether its first
// control is the one read at show (and, with place, still where it was).
func (h *Host) stillFrontLocked(place bool) bool {
	now, err := h.p.Front()
	if err != nil || now == nil || now.Pid != h.pid || len(now.Targets) == 0 {
		return false
	}
	first := now.Targets[0]
	if first.ID != h.first {
		return false
	}
	if !place {
		return true
	}
	sameWindow := (now.Window == nil) == (h.windowAt == nil) &&
		(now.Window == nil || *now.Window == *h.windowAt)
	return first.Bounds == h.firstAt && sameWindow
}

// OnWindowFrameChanged takes the badges down when the badged window moves
// or resizes, which leaves every badge off its control. The event names a
// window by an id and a frame from the window layer, which neither matches
// the controls read (the id is another id space; the frame includes
// decorations on some systems), so the front is read again and the badges
// stay only while its first control is the same element at the same place.
// Another window moving moves none of these controls.
func (h *Host) OnWindowFrameChanged() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.moved++
	if h.open && !h.stillFrontLocked(true) {
		h.closeLocked("window_moved")
	}
}

func renderMessage(msg string) string {
	return `<div class="badges-message">` + html.EscapeString(msg) + `</div>`
}
