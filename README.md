<img src="site/assets/favicon.svg" width="30" align="left" alt="">

# whatsapp-gui

Standalone Qt Quick WhatsApp client for Omarchy. It reads the local `wacli`
mirror, sends through `wacli`, and follows `~/.local/state/omarchy/current/theme`.

&nbsp;

**Website & Documentation:** [https://parnoldx.github.io/whatsapp-gui/](https://parnoldx.github.io/whatsapp-gui/)

## Install (from source)

```sh
git clone https://github.com/parnoldx/whatsapp-gui && cd whatsapp-gui
make dependencies   # once: build deps via pacman
make install
```

Installs to `~/.local` — `whatsapp-gui` and the bundled `wacli` helper. Run
`whatsapp-gui --toggle` or bind it to a key. Closing hides the window; the
process stays so the next open is instant. `Ctrl+Q` quits.

## First run: pairing

The GUI has no pairing screen — pair once with the bundled `wacli` CLI
(installed to `~/.local/bin` by `make install`):

```sh
wacli auth                        # scan the terminal QR code: WhatsApp → Linked devices
```

After the first sync, launch `whatsapp-gui` — it finds the existing wacli
store automatically and stays paired until you unlink the device in WhatsApp.

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

## Development

```sh
tests/run        # Go helper tests + QML model tests
make install     # rebuild + reinstall to ~/.local
```

Vendored `wacli/` (trimmed to the helper and its internals) is MIT licensed by
its upstream authors; the rest of this repo is MIT — see `LICENSE`.
