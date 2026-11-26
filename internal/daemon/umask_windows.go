package daemon

// Windows has no umask; the control socket lives in the user's own
// %APPDATA% folder, which other users cannot access.
func umask(m int) int { return 0 }
