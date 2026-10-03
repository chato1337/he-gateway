package autocert

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenBrowser opens url in the default browser and returns without waiting
// for the browser to exit.
func OpenBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	case "darwin":
		cmd = exec.Command("open", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open %s: %w", rawURL, err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
