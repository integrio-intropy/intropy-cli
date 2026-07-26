package dashboard

import (
	"os/exec"
	"runtime"
)

// openBrowser opens url in the user's default browser. It is best-effort: the
// caller treats any error as a warning, never a failure.
func openBrowser(url string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name, args = "open", []string{url}
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		name, args = "xdg-open", []string{url}
	}
	return exec.Command(name, args...).Start()
}
