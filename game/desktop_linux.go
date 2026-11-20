package main

import (
	"errors"
	"fmt"
)

func desktop(url, profile string) error { return errors.New("desktop window is Windows only") }
func showError(s string)                { fmt.Println(s) }
func resizeDesktop(p Preferences)       {}
