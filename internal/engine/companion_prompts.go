package engine

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/adkulas/homelab/internal/companions"
	"golang.org/x/term"
)

func promptCompanionAnswers(reader *bufio.Reader, input io.Reader, output io.Writer, answers *initAnswers, skipCleanup bool) error {
	for _, account := range []struct {
		name               string
		username, password *string
	}{{"Houndarr", &answers.HoundarrUsername, &answers.HoundarrPassword}, {"Cleanuparr", &answers.CleanuparrUsername, &answers.CleanuparrPassword}} {
		if account.name == "Cleanuparr" && skipCleanup {
			continue
		}
		if *account.username == "" {
			value, err := promptRequired(reader, output, account.name+" administrator username: ")
			if err != nil {
				return err
			}
			*account.username = value
		}
		if *account.password == "" {
			value, err := promptHiddenPassword(reader, input, output, account.name+" administrator password: ")
			if err != nil {
				return err
			}
			confirmation, err := promptHiddenPassword(reader, input, output, "Confirm "+account.name+" administrator password: ")
			if err != nil {
				return err
			}
			if value != confirmation {
				return fmt.Errorf("%s password confirmation does not match", account.name)
			}
			*account.password = value
		}
		if err := companions.ValidateAdminCredentials(account.name, companions.AdminCredentials{Username: *account.username, Password: *account.password}); err != nil {
			return err
		}
	}
	return nil
}

func promptHiddenPassword(reader *bufio.Reader, input io.Reader, output io.Writer, label string) (string, error) {
	fmt.Fprint(output, label)
	var value string
	if file, ok := input.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		encoded, err := term.ReadPassword(int(file.Fd()))
		fmt.Fprintln(output)
		if err != nil {
			return "", fmt.Errorf("read hidden password: %w", err)
		}
		value = string(encoded)
	} else {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		value = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("a password is required")
	}
	return value, nil
}
