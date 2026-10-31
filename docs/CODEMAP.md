# Code map

- `main.go`: XDG data path, global `--data`, oden dispatch and ochazuke CLI mount.
- `app.go`: app runtime and catalog declarations.
- `settings.go`: public settings and safe defaults.
- `app_test.go`: isolated catalog/CLI tests.
- `Makefile`: bounded Go build/test and coordinator race gate.

Compared with Vim: not applicable; lanes are domain records. Future tree motions use ochazuke's existing behaviour.
