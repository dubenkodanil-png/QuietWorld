package main

import "errors"

func startUpdater(helper, self, next, version string) error {
	return errors.New("Обновление поддерживается только в Windows")
}
func requestUpdateExit()        {}
func applyUpdate(args []string) {}
