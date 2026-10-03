//go:build darwin

package autocert

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type systemInstaller struct{}

// SystemInstaller adds the authority to the login keychain. macOS asks for
// the keychain password in its own window.
func SystemInstaller() Installer { return systemInstaller{} }

func (systemInstaller) Installed(ctx context.Context, caPEM []byte) (bool, error) {
	thumb, err := thumbprint(caPEM)
	if err != nil {
		return false, err
	}
	keychain, err := loginKeychain()
	if err != nil {
		return false, err
	}
	cmd := exec.CommandContext(ctx, "security", "find-certificate", "-a", "-Z", "-c", caCommonName, keychain)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	return strings.Contains(strings.ToLower(string(out)), thumb), nil
}

func (systemInstaller) Install(ctx context.Context, caPath string) error {
	keychain, err := loginKeychain()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "security", "add-trusted-cert", "-r", "trustRoot", "-k", keychain, caPath)
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
	cmd := exec.Command("security", "add-trusted-cert", "-d", "-r", "trustRoot", "-k", "/Library/Keychains/System.keychain", caPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("trust local ca: %w", err)
	}
	return nil
}

func loginKeychain() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home directory: %w", err)
	}
	current := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
	if _, err := os.Stat(current); err == nil {
		return current, nil
	}
	return filepath.Join(home, "Library", "Keychains", "login.keychain"), nil
}

func thumbprint(caPEM []byte) (string, error) {
	block, _ := pem.Decode(caPEM)
	if block == nil {
		return "", fmt.Errorf("parse local ca")
	}
	sum := sha1.Sum(block.Bytes)
	return hex.EncodeToString(sum[:]), nil
}
