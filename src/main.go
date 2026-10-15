// Badges: act on any control in the window in front by voice.
//
// "show badges" asks the platform for the window's controls
// (native.ax_targets) and publishes them, with their places, as the rows of
// the badged `@targets` selection surface. The platform assigns each a code
// word and draws it on the control; saying a code word runs badges.press,
// which acts on the control through native.ax_perform_action.
package main

import (
	"encoding/json"

	"github.com/branchkit/plugin-sdk-go"
)

// sdkPlatform answers the platform seam with the typed SDK wrappers.
type sdkPlatform struct{ p *branchkit.Plugin }

func (s sdkPlatform) Targets() (*branchkit.NativeAxTargetsResponse, error) {
	return s.p.NativeAxTargets(branchkit.NativeAxTargetsRequest{})
}

func (s sdkPlatform) Perform(handle, action string) (bool, error) {
	return s.p.NativeAxPerformAction(branchkit.NativeAxPerformActionRequest{
		Element: branchkit.AccessibleRef{ID: &handle},
		Action:  action,
	})
}

func (s sdkPlatform) ReplaceRows(rows []branchkit.CollectionPutEntry) error {
	if rows == nil {
		rows = []branchkit.CollectionPutEntry{}
	}
	_, err := s.p.Replace(rowsCollection, rows, branchkit.ScopeCollection(), branchkit.WithReplaceRoles(rowRoles))
	return err
}

func (s sdkPlatform) SurfaceGates() ([]string, error) {
	records, err := s.p.ListAll("_surface")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(records))
	for _, rec := range records {
		var v struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(rec.Payload, &v) == nil && v.Name != "" {
			names = append(names, v.Name)
		}
	}
	return names, nil
}

func (s sdkPlatform) Message(text string) {
	if err := s.p.HUDPushFragment(hudChannel, "content", renderMessage(text)); err != nil {
		branchkit.Logf(pluginID, "hud.push: %v", err)
		return
	}
	if err := s.p.HUDShow(branchkit.HUDShowRequest{Channel: hudChannel}); err != nil {
		branchkit.Logf(pluginID, "hud.show: %v", err)
	}
}

func (s sdkPlatform) HideMessage() {
	if err := s.p.HUDHide(branchkit.HUDHideRequest{Channel: hudChannel}); err != nil {
		branchkit.Logf(pluginID, "hud.hide: %v", err)
	}
}

func main() {
	p := branchkit.NewPlugin()
	h := newHost(sdkPlatform{p: p})

	HandleShow(p, func(_ *branchkit.OnActionRequest) (any, error) {
		return nil, h.Show()
	})
	HandleHide(p, func(_ *branchkit.OnActionRequest) (any, error) {
		h.Hide()
		return nil, nil
	})
	HandlePress(p, func(params PressParams, _ *branchkit.OnActionRequest) (any, error) {
		action := ""
		if params.Action != nil {
			action = *params.Action
		}
		return nil, h.Press(params.Handle, action)
	})
	p.Subscribe("_surface", func(_ branchkit.CollectionUpdatedEventParams) { h.OnSurfaceChanged() })
	p.On(branchkit.EventWindowFocused, func(json.RawMessage) { h.OnWindowFocused() })
	p.On(branchkit.EventWindowFrameChanged, func(params json.RawMessage) {
		var ev branchkit.WindowFrameChangedEventParams
		if json.Unmarshal(params, &ev) == nil {
			h.OnWindowFrameChanged(ev.Old)
		}
	})

	p.Run()
}
