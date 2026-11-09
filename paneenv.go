package main

// paneEnv reads a pane variable by its bare name (PANE_ID): mtm's MTM_<name>
// first, then the TUIOS_<name> mtm sets beside it for one release.
func paneEnv(get func(string) string, name string) string {
	if v := get("MTM_" + name); v != "" {
		return v
	}
	return get("TUIOS_" + name)
}
