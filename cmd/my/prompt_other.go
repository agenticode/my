//go:build !linux && !darwin

package main

import (
	"context"
	"errors"

	"github.com/agenticode/my/internal/cli"
)

// promptPassword asks for a password on the terminal. On this platform
// there is no way to read one without showing it.
func promptPassword(context.Context, *cli.Env, string) (string, error) {
	return "", errors.New("no terminal to ask for a password on")
}
