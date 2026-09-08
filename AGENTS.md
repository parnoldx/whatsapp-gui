# AGENTS.md

## After implementing anything new

The user runs the installed app from `~/.local/bin`, not the build dir. After every change, rebuild, reinstall, and relaunch so the user can review it live:

```sh
make install launch
```

That kills the running instance, rebuilds, installs to `~/.local`, and launches the new binary. Always do this before reporting done.
