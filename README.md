# whatsapp-gui

Standalone Qt Quick WhatsApp client for Omarchy. It reads the local `wacli`
mirror, sends through `wacli`, and follows `~/.local/state/omarchy/current/theme`.

```bash
cmake -B build -G Ninja
cmake --build build
cmake --install build --prefix ~/.local
whatsapp-gui --toggle
```

`Super+Shift+G` toggles the window. Closing hides it; the process stays so the
next open is instant. `Ctrl+Q` quits.

## Blade mode

The window is sized as a right-edge blade (560px wide, full height). Hyprland
does the sliding — add these rules to `~/.config/hypr/looknfeel.lua`:

```lua
o.window("^(whatsapp-gui)$", { name = "blade-whatsapp", float = true })
o.window("^(whatsapp-gui)$", { size = { 560, "monitor_h - 26" } })
o.window("^(whatsapp-gui)$", { move = { "monitor_w - 560", 26 } })
o.window("^(whatsapp-gui)$", { animation = "slide right" })
o.window("^(whatsapp-gui)$", { rounding = 0 })
```

`560` is the blade width (it appears in both `size` and `move`), `26` the
bar's reserved strip (`hyprctl monitors`). Edit the file and `hyprctl reload`
to retune — `hyprctl keyword` refuses to touch rules under the Lua parser.
Use `monitor_w - <width>` for the x offset; `100%-w` is not understood here.
Toggling hides and reshows the window, which remaps the surface, so Hyprland
replays its `windowsIn`/`windowsOut` slide each time.

`Esc` goes back to the chat list, or sheathes the blade when no chat is open.
