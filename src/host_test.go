package main

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/branchkit/plugin-sdk-go"
)

// fake records what the plugin asks of the app.
type fake struct {
	targets  *branchkit.NativeAxTargetsResponse
	readErr  error
	rows     [][]branchkit.CollectionPutEntry
	pressed  [][2]string
	pressOK  bool
	gates    []string
	messages []string
	hidden   int
	states   []branchkit.OutputState
	cleared  int
	// output is the order of Message ("message"), PublishState ("state")
	// and ClearState ("clear") calls.
	output []string
	// duringRead runs inside Targets: an event arriving mid-read.
	duringRead func()
	// front is what is in front when asked again; nil means targets.
	front *branchkit.NativeAxTargetsResponse
	// replaceErr fails the next ReplaceRows (it is still recorded).
	replaceErr error
}

func (f *fake) Targets() (*branchkit.NativeAxTargetsResponse, error) {
	if f.duringRead != nil {
		f.duringRead()
	}
	return f.targets, f.readErr
}
func (f *fake) Front() (*branchkit.NativeAxTargetsResponse, error) {
	if f.front != nil {
		return f.front, nil
	}
	return f.targets, f.readErr
}
func (f *fake) Perform(handle, action string) (bool, error) {
	f.pressed = append(f.pressed, [2]string{handle, action})
	return f.pressOK, nil
}
func (f *fake) ReplaceRows(rows []branchkit.CollectionPutEntry) error {
	f.rows = append(f.rows, rows)
	err := f.replaceErr
	f.replaceErr = nil
	return err
}
func (f *fake) SurfaceGates() ([]string, error) { return f.gates, nil }
func (f *fake) Message(text string) {
	f.messages = append(f.messages, text)
	f.output = append(f.output, "message")
}
func (f *fake) HideMessage() { f.hidden++ }
func (f *fake) PublishState(doc branchkit.OutputState) {
	f.states = append(f.states, doc)
	f.output = append(f.output, "state")
}
func (f *fake) ClearState() {
	f.cleared++
	f.output = append(f.output, "clear")
}

func target(id, role string, name *string, x, y int) branchkit.AxTarget {
	return branchkit.AxTarget{
		ID: id, Role: role, Name: name, Action: "press",
		Bounds: branchkit.AccessibleBounds{X: x, Y: y, Width: 80, Height: 24},
	}
}

func window() *branchkit.NativeAxTargetsResponse {
	save := "Save"
	return &branchkit.NativeAxTargetsResponse{Targets: []branchkit.AxTarget{
		target("h1", "button", &save, 10, 20),
		// A password field: its name is withheld, so the row says its role.
		{ID: "h2", Role: "textbox", Action: "focus", Protected: true,
			Bounds: branchkit.AccessibleBounds{X: 10, Y: 60, Width: 200, Height: 24}},
	}}
}

func newTestHost(f *fake) *Host {
	h := newHost(f)
	h.messageFor = time.Hour
	return h
}

func TestShowPublishesEachControlWithItsPlaceInReadingOrder(t *testing.T) {
	f := &fake{targets: window()}
	if err := newTestHost(f).Show(); err != nil {
		t.Fatal(err)
	}
	if len(f.rows) != 1 || len(f.rows[0]) != 2 {
		t.Fatalf("rows = %v", f.rows)
	}
	var first, second map[string]any
	_ = json.Unmarshal(f.rows[0][0].Payload, &first)
	_ = json.Unmarshal(f.rows[0][1].Payload, &second)
	if first["name"] != "Save" || first["rank"] != 0.0 || first["x"] != 10.0 || first["y"] != 20.0 ||
		first["w"] != 80.0 || first["h"] != 24.0 || first["handle"] != "h1" {
		t.Errorf("first row = %v", first)
	}
	if second["name"] != "textbox" || second["rank"] != 1.0 || second["action"] != "focus" {
		t.Errorf("a password field is named by its role: %v", second)
	}
}

func TestNothingToBadgeSaysSoAndPublishesNothing(t *testing.T) {
	f := &fake{targets: &branchkit.NativeAxTargetsResponse{}}
	_ = newTestHost(f).Show()
	if len(f.rows) != 0 || len(f.messages) != 1 {
		t.Errorf("rows %v, messages %v", f.rows, f.messages)
	}
	f = &fake{readErr: errors.New("no window")}
	_ = newTestHost(f).Show()
	if len(f.rows) != 0 || len(f.messages) != 1 {
		t.Errorf("rows %v, messages %v", f.rows, f.messages)
	}
}

func TestPressActsWithTheControlsOwnActionThenTakesTheBadgesDown(t *testing.T) {
	f := &fake{targets: window(), pressOK: true}
	h := newTestHost(f)
	_ = h.Show()
	if err := h.Press("h2", ""); err != nil {
		t.Fatal(err)
	}
	if len(f.pressed) != 1 || f.pressed[0] != [2]string{"h2", "focus"} {
		t.Errorf("pressed %v", f.pressed)
	}
	if len(f.rows) != 2 || len(f.rows[1]) != 0 {
		t.Errorf("the rows drain after a press: %v", f.rows)
	}
	if len(f.messages) != 0 {
		t.Errorf("no message on success: %v", f.messages)
	}
}

func TestAControlThatDoesNotRespondSaysSo(t *testing.T) {
	f := &fake{targets: window(), pressOK: false}
	h := newTestHost(f)
	_ = h.Show()
	_ = h.Press("h1", "")
	if len(f.messages) != 1 {
		t.Errorf("messages %v", f.messages)
	}
}

func TestBadgesComeDownWhenTheSurfaceEndsElsewhere(t *testing.T) {
	f := &fake{targets: window(), gates: []string{surfaceGateName}}
	h := newTestHost(f)
	_ = h.Show()
	h.OnSurfaceChanged()
	if len(f.rows) != 1 {
		t.Fatalf("still open while its gate is set: %v", f.rows)
	}
	f.gates = []string{"browser.palette"}
	h.OnSurfaceChanged()
	if len(f.rows) != 2 || len(f.rows[1]) != 0 {
		t.Errorf("drained once its gate is gone: %v", f.rows)
	}
	h.Hide() // already closed: nothing more to drain
	if len(f.rows) != 2 {
		t.Errorf("rows %v", f.rows)
	}
}

func TestAFailedPublishStillComesDown(t *testing.T) {
	f := &fake{targets: window(), replaceErr: errors.New("timed out")}
	h := newTestHost(f)
	if err := h.Show(); err == nil {
		t.Fatal("the failed publish is reported")
	}
	// The rows may have landed after the call gave up: hiding drains them.
	h.Hide()
	if len(f.rows) != 2 || len(f.rows[1]) != 0 {
		t.Errorf("drained after a failed publish: %v", f.rows)
	}
}

func TestBadgesComeDownWhenAnotherWindowComesToTheFront(t *testing.T) {
	badged := window()
	badged.Pid = 7
	badged.Window = &branchkit.AccessibleBounds{X: 100, Y: 50, Width: 800, Height: 600}
	f := &fake{targets: badged}
	h := newTestHost(f)
	h.OnWindowFocused() // nothing showing: nothing to drain
	if len(f.rows) != 0 {
		t.Fatalf("rows %v", f.rows)
	}
	_ = h.Show()
	// The poll's late report of the switch to this very window: kept.
	h.OnWindowFocused()
	if len(f.rows) != 1 {
		t.Fatalf("drained by the report of its own window: %v", f.rows)
	}
	// Another window of the same application, with the same frame (both
	// filling the display): its controls are other elements. Drained.
	save := "Save"
	f.front = &branchkit.NativeAxTargetsResponse{Pid: 7, Window: badged.Window,
		Targets: []branchkit.AxTarget{target("h9", "button", &save, 10, 20)}}
	h.OnWindowFocused()
	if len(f.rows) != 2 || len(f.rows[1]) != 0 {
		t.Errorf("drained when focus moved: %v", f.rows)
	}
}

func TestBadgesComeDownWhenTheirWindowMoves(t *testing.T) {
	f := &fake{targets: window()}
	h := newTestHost(f)
	_ = h.Show()
	// Another window moved: the badged window's controls did not.
	h.OnWindowFrameChanged()
	if len(f.rows) != 1 {
		t.Fatalf("another window's move drained them: %v", f.rows)
	}
	// Their own window moved: its first control is elsewhere now.
	save := "Save"
	f.front = &branchkit.NativeAxTargetsResponse{
		Targets: []branchkit.AxTarget{target("h1", "button", &save, 210, 120)}}
	h.OnWindowFrameChanged()
	if len(f.rows) != 2 || len(f.rows[1]) != 0 {
		t.Errorf("drained when their window moved: %v", f.rows)
	}
}

func TestAFocusChangeDuringTheReadIsCheckedAfterPublishing(t *testing.T) {
	f := &fake{targets: window()}
	h := newTestHost(f)
	save := "Save"
	f.duringRead = func() {
		// Another window comes to the front while the window is read.
		f.front = &branchkit.NativeAxTargetsResponse{Pid: 9,
			Targets: []branchkit.AxTarget{target("h9", "button", &save, 0, 0)}}
		go h.OnWindowFocused()
		for {
			h.mu.Lock()
			n := h.moved
			h.mu.Unlock()
			if n > 0 {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}
	_ = h.Show()
	if len(f.rows) != 2 || len(f.rows[1]) != 0 {
		t.Errorf("drained after publishing, since focus moved mid-read: %v", f.rows)
	}
}

func TestBadgesComeDownWhenTheirWindowResizesAroundItsFirstControl(t *testing.T) {
	shown := window()
	shown.Window = &branchkit.AccessibleBounds{X: 0, Y: 0, Width: 800, Height: 600}
	f := &fake{targets: shown}
	h := newTestHost(f)
	_ = h.Show()
	// Dragged from the far corner: the first control stayed put.
	resized := window()
	resized.Window = &branchkit.AccessibleBounds{X: 0, Y: 0, Width: 500, Height: 400}
	f.front = resized
	h.OnWindowFrameChanged()
	if len(f.rows) != 2 || len(f.rows[1]) != 0 {
		t.Errorf("drained when their window resized: %v", f.rows)
	}
}

// Each message the window shows is published as a problem in the same
// words, on the window's own channel, so speech and the window's
// accessibility element say what the pixels say.
func TestEachMessageIsPublishedAsAProblemInTheSameWords(t *testing.T) {
	cases := map[string]func(f *fake, h *Host){
		"read failed": func(f *fake, h *Host) {
			f.readErr = errors.New("no window")
			_ = h.Show()
		},
		"nothing found": func(f *fake, h *Host) {
			f.targets = &branchkit.NativeAxTargetsResponse{}
			_ = h.Show()
		},
		"no response": func(f *fake, h *Host) {
			f.targets = window()
			_ = h.Show()
			_ = h.Press("h1", "")
		},
	}
	for name, run := range cases {
		f := &fake{}
		run(f, newTestHost(f))
		if len(f.messages) != 1 || len(f.states) != 1 {
			t.Errorf("%s: messages %v, states %v", name, f.messages, f.states)
			continue
		}
		doc := f.states[0]
		if doc.Kind != branchkit.OutputKindProblem || doc.Channel != hudChannel ||
			doc.Phrase != f.messages[0] || doc.Title != f.messages[0] ||
			doc.Locale == "" || doc.V != 1 {
			t.Errorf("%s: state %+v for message %q", name, doc, f.messages[0])
		}
	}
}

func TestTheMessagesStateIsClearedWhenBadgesComeUp(t *testing.T) {
	f := &fake{readErr: errors.New("no window")}
	h := newTestHost(f)
	_ = h.Show()
	f.readErr, f.targets = nil, window()
	f.output = nil
	_ = h.Show()
	if len(f.output) != 1 || f.output[0] != "clear" {
		t.Errorf("output %v when badges replaced the message", f.output)
	}
	// With no message published, nothing is cleared.
	f.output = nil
	_ = h.Show()
	if len(f.output) != 0 {
		t.Errorf("output %v with no message up", f.output)
	}
}

// The platform reports a state with the same meaning as the one it holds
// as no change, and speech is silent for it: the same message again must
// still be said, so each one clears what the channel held first. The clear
// comes before the drawing, since a clear also blanks the window.
func TestTheSameMessageAgainIsANewState(t *testing.T) {
	f := &fake{targets: &branchkit.NativeAxTargetsResponse{}}
	h := newTestHost(f)
	_ = h.Show()
	_ = h.Show()
	want := []string{"clear", "message", "state", "clear", "message", "state"}
	if len(f.output) != len(want) {
		t.Fatalf("output %v, want %v", f.output, want)
	}
	for i := range want {
		if f.output[i] != want[i] {
			t.Fatalf("output %v, want %v", f.output, want)
		}
	}
}

// The window's time is a reading time: when it runs out the window goes,
// but the state stays, so speech still saying the message is not cut off.
func TestTheMessagesTimeHidesTheWindowButKeepsItsState(t *testing.T) {
	f := &fake{targets: &branchkit.NativeAxTargetsResponse{}}
	h := newHost(f)
	h.messageFor = time.Millisecond
	_ = h.Show()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.mu.Lock()
		hidden := f.hidden
		h.mu.Unlock()
		if hidden == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the window did not hide when its time ran out")
		}
		time.Sleep(time.Millisecond)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if want := []string{"clear", "message", "state"}; len(f.output) != 3 || f.output[2] != "state" {
		t.Errorf("output %v when only the window's time ran out, want %v", f.output, want)
	}
}
