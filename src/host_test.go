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
}

func (f *fake) Targets() (*branchkit.NativeAxTargetsResponse, error) { return f.targets, f.readErr }
func (f *fake) Perform(handle, action string) (bool, error) {
	f.pressed = append(f.pressed, [2]string{handle, action})
	return f.pressOK, nil
}
func (f *fake) ReplaceRows(rows []branchkit.CollectionPutEntry) error {
	f.rows = append(f.rows, rows)
	return nil
}
func (f *fake) SurfaceGates() ([]string, error) { return f.gates, nil }
func (f *fake) Message(text string)             { f.messages = append(f.messages, text) }
func (f *fake) HideMessage()                    { f.hidden++ }

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
