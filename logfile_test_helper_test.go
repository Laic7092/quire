package main

import (
	"log"
	"os"
	"testing"
)

func writeLog(s string) { log.Print(s) }

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}
