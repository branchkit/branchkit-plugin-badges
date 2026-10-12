# Badges

Act on any control in the window in front by voice.

Say **"show badges"** (or "show badge"). A code word appears on each of the
window's buttons, links, menu items, checkboxes, switches, tabs, pop-ups,
fields and sliders. Say a code word to act on that control: a button is
pressed, a field focused, a checkbox toggled. Say **"hide badges"** or
**"over"** to take the badges down; **"show badges"** while they are up reads
the window again.

How it works: the plugin asks BranchKit for the controls of the window in
front (`native.ax_targets`) and publishes them, with where each is on screen,
as the rows of a badged selection surface. BranchKit assigns the code words,
draws them, and makes them speakable; when one is said, the plugin acts on
that control (`native.ax_perform_action`). The plugin never draws text: the
words and their look are BranchKit's.

Limits of this version:

- One press per "show badges": pressing a control takes the badges down, so
  the next "show badges" reads the window as it is then.
- At most 200 controls. A web page, or an Electron app's document, is read
  after the window's own controls.
- Badges are drawn on macOS, Windows and X11. GNOME on Wayland gives
  BranchKit no way to draw over other windows yet, so they do not appear
  there.

Privacy: the window is read only when you ask, and only the window in front.
A password field's name is never read, and names are never logged. Nothing
is stored: the list lives in memory while the badges are up.

Privileges: `accessibility` (read and act on another application's controls)
and `overlay` (the badges are drawn over the screen).

MIT.
