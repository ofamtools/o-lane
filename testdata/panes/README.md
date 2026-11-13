# tuios list samples

Captured on 2026-10-31 with `tuios list-sessions --json` and
`tuios list-windows --json -s hub`. Sessions are a top-level array; windows
are inside an object with command metadata. Keep only fields relevant to the
tests and representative extra metadata. Normalize identifiers and titles;
omit paths, process details and agent messages.

The sessions object sample wraps the captured rows under `sessions` to cover
the previous CLI format. The windows array sample unwraps `windows` to cover
the alternate collection format. Neither conversion changes the row fields.
