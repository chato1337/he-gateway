//go:build windows

package autocert

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type systemInstaller struct{}

// SystemInstaller talks to the Windows certificate store through certutil.
func SystemInstaller() Installer { return systemInstaller{} }

func (systemInstaller) Installed(ctx context.Context, caPEM []byte) (bool, error) {
	thumb, err := thumbprint(caPEM)
	if err != nil {
		return false, err
	}
	user, err := certutilHas(ctx, true, thumb)
	if err != nil || user {
		return user, err
	}
	return certutilHas(ctx, false, thumb)
}

func (systemInstaller) Install(ctx context.Context, caPath string) error {
	out, err := exec.CommandContext(ctx, "certutil", "-user", "-addstore", "Root", caPath).CombinedOutput()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if userCancelled(err, out) {
		return fmt.Errorf("local ca was not trusted: %w", err)
	}
	if err := elevate(ctx, caPath); err != nil {
		return err
	}
	return nil
}

func installMachine(caPath string) error {
	out, err := exec.Command("certutil", "-addstore", "Root", caPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func certutilHas(ctx context.Context, user bool, thumb string) (bool, error) {
	args := []string{"-verifystore", "Root", thumb}
	if user {
		args = append([]string{"-user"}, args...)
	}
	cmd := exec.CommandContext(ctx, "certutil", args...)
	if err := cmd.Run(); err == nil {
		return true, nil
	} else if errors.Is(err, exec.ErrNotFound) {
		return false, fmt.Errorf("certutil: %w", err)
	}
	return false, nil
}

func elevate(ctx context.Context, caPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find gateway executable: %w", err)
	}
	script := fmt.Sprintf(
		"$p = Start-Process -FilePath %s -ArgumentList @(%s, %s) -Verb RunAs -Wait -PassThru; if ($null -eq $p) { exit 1 }; exit $p.ExitCode",
		psQuote(exe), psQuote("-install-ca"), psQuote(caPath),
	)
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("elevate ca install: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

func thumbprint(caPEM []byte) (string, error) {
	block, _ := pem.Decode(caPEM)
	if block == nil {
		return "", fmt.Errorf("parse local ca")
	}
	sum := sha1.Sum(block.Bytes)
	return strings.ToUpper(hex.EncodeToString(sum[:])), nil
}

func userCancelled(err error, out []byte) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := uint32(exitErr.ExitCode())
		if code == 1223 || code == 0x800704C7 {
			return true
		}
	}
	text := strings.ToLower(string(out))
	return strings.Contains(text, "cancel")
}

func psQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
