// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"io"
	"log"

	signalLog "go.mau.fi/libsignal/logger"
)

// Signal's default logger writes to stdout, which would both expose account
// state and corrupt the private protocol. Configure it before any client exists.
type silentSignalLog struct{}

func (silentSignalLog) Debug(string, string)   {}
func (silentSignalLog) Info(string, string)    {}
func (silentSignalLog) Warning(string, string) {}
func (silentSignalLog) Error(string, string)   {}
func (silentSignalLog) Configure(string)       {}
func init() {
	signalLog.Logger = silentSignalLog{}
	log.SetOutput(io.Discard)
}
