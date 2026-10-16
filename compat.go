package main

import (
	"net/url"
	"os/exec"
)

var url_Parse = url.Parse

var exec_Command = func(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}
