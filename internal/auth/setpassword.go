package auth

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/term"
)

// SetPassword interactively prompts for a new admin password, bcrypt-hashes it
// at cost 12, and saves it to the config file at configPath.
// It reads from /dev/tty directly so it works correctly even when stdin is
// piped (e.g. inside install.sh).
func SetPassword(configPath string) error {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open /dev/tty: %w", err)
	}
	defer tty.Close()

	fd := int(tty.Fd())

	fmt.Fprint(tty, "New password: ")
	pass1, err := term.ReadPassword(fd)
	fmt.Fprintln(tty)
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	if len(pass1) == 0 {
		return errors.New("password must not be empty")
	}

	fmt.Fprint(tty, "Confirm password: ")
	pass2, err := term.ReadPassword(fd)
	fmt.Fprintln(tty)
	if err != nil {
		return fmt.Errorf("read confirmation: %w", err)
	}

	if string(pass1) != string(pass2) {
		return errors.New("passwords do not match")
	}

	hash, err := hashPassword(pass1)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cfg.PasswordHash = hash

	if err := SaveConfig(configPath, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Fprintln(tty, "Password updated.")
	return nil
}
