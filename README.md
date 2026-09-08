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
