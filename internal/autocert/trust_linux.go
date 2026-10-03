//go:build linux

package autocert

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

const linuxCAPath = "/usr/local/share/ca-certificates/he-gateway.crt"

type systemInstaller struct{}

// SystemInstaller asks PolicyKit once to copy the authority into the system
// certificate directory and refresh the bundle.
func SystemInstaller() Installer { return systemInstaller{} }

func (systemInstaller) Installed(ctx context.Context, caPEM []byte) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return sameFile(linuxCAPath, caPEM)
}

func (systemInstaller) Install(ctx context.Context, caPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find gateway executable: %w", err)
	}
	cmd := exec.CommandContext(ctx, "pkexec", exe, "-install-ca", caPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("trust local ca: %w", err)
	}
	return nil
}

func installMachine(caPath string) error {
	body, err := os.ReadFile(caPath)
	if err != nil {
		return fmt.Errorf("read local ca: %w", err)
	}
	if err := os.MkdirAll("/usr/local/share/ca-certificates", 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(linuxCAPath, body, 0o644); err != nil {
		return fmt.Errorf("write system ca: %w", err)
	}
	cmd := exec.Command("update-ca-certificates")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

func sameFile(path string, pemBytes []byte) (bool, error) {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return bytes.Equal(bytes.TrimSpace(body), bytes.TrimSpace(pemBytes)), nil
}
