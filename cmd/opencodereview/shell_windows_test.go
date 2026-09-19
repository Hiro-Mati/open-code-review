//go:build windows

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestShellCommand_CmdLine pins that the setup script reaches cmd.exe through
// SysProcAttr.CmdLine verbatim instead of through Args, whose
// syscall.EscapeArg quoting turns embedded double quotes into \" sequences that
// cmd.exe does not understand.
func TestShellCommand_CmdLine(t *testing.T) {
	c := shellCommand(context.Background(), `"C:\Program Files\tool\x.exe" --flag`)

	want := `cmd.exe /S /C ""C:\Program Files\tool\x.exe" --flag"`
	if c.SysProcAttr == nil {
		t.Fatal("SysProcAttr is nil; the command line would be built from Args instead")
	}
	if got := c.SysProcAttr.CmdLine; got != want {
		t.Errorf("CmdLine = %q, want %q", got, want)
	}
	if len(c.Args) == 0 {
		t.Error("Args is empty; Cmd.String() indexes Args[1:] and panics on a nil slice")
	}
	if s := c.String(); s == "" {
		t.Error("Cmd.String() returned empty")
	}
}

// TestShellCommand_QuotedScripts runs real setup scripts through cmd.exe and
// checks that double quotes survive, including the common shape of a quoted
// executable path containing a space followed by arguments.
func TestShellCommand_QuotedScripts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "with space")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "setup.cmd")
	if err := os.WriteFile(script, []byte("@echo setup ran %1\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		script string
		want   string
	}{
		{name: "plain", script: "echo plain", want: "plain"},
		{name: "quoted argument", script: `echo "a b"`, want: `"a b"`},
		{name: "quoted executable path with space", script: `"` + script + `" hello`, want: "setup ran hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := shellCommand(context.Background(), tt.script)
			out, err := c.CombinedOutput()
			if err != nil {
				t.Fatalf("script %q failed: %v\noutput: %s", tt.script, err, out)
			}
			if got := strings.TrimSpace(string(out)); got != tt.want {
				t.Errorf("script %q output = %q, want %q", tt.script, got, tt.want)
			}
		})
	}
}
