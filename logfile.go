package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
)

// logToFile sends the process's log to the file whose path it is given, and
// returns the function that puts it back.
//
// A phone has no console: a sideloaded build's standard error goes nowhere, so
// MyGo's frame statistics — the one place a frame's build, layout, paint and
// present are told apart — cannot be read on the device that is slow. A file
// beside the reader's state can be read, and that is what this is for.
//
// A path that cannot be written is not an error worth stopping for: the log
// goes on to the console, which is what a desk has.
func logToFile(path string) func() {
	if path == "" {
		return func() {}
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return func() {}
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return func() {}
	}
	old := log.Writer()
	log.SetOutput(io.MultiWriter(old, f))
	return func() {
		log.SetOutput(old)
		_ = f.Close()
	}
}

// nextToState is the log's path for a state file: beside it, named after it.
// On a phone it is in the app's Documents instead: that is the one directory
// the system will show (with UIFileSharingEnabled), and the state's own
// directory — under Library — is one nothing can open.
func nextToState(statePath string) string {
	if dir := documents(); dir != "" {
		return filepath.Join(dir, "quire.log")
	}
	if statePath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(statePath), "quire.log")
}

// documents is the app's Documents directory, when the process is one whose
// sandbox has it.
func documents() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	dir := filepath.Join(home, "Documents")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return ""
	}
	return dir
}
