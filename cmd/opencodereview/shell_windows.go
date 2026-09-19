//go:build windows

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"context"
	"os/exec"
	"syscall"
)

// shellCommand runs an MCP server setup script through cmd.exe /C, honoring ctx
// cancellation. It mirrors newKeyCmd in internal/llm/keycmd_windows.go; see that
// file for the full rationale.
//
// In short: os/exec quotes Args with syscall.EscapeArg, which targets
// CommandLineToArgvW, but cmd.exe has its own unquoting rules and does not
// understand the resulting \" escapes. A setup script such as
// `"C:\Program Files\tool\x.exe" arg` would therefore reach cmd.exe mangled and
// fail, and the MCP server would be skipped. The command line is instead handed
// over verbatim through SysProcAttr.CmdLine; /S makes cmd.exe strip exactly the
// outer pair of quotes added here and run the rest unchanged.
//
// The script is deliberately not escaped: it is a command line the config author
// asked us to run (the Unix arm hands the same string to `sh -c`), and escaping
// the inner quotes would defeat the case CmdLine exists for.
//
// The executable is spelled with its extension, as in keycmd_windows.go, so a
// file named `cmd` on PATH cannot shadow the shell. Callers that later touch
// SysProcAttr must modify the existing struct rather than replace it, or the
// CmdLine set here is lost.
func shellCommand(ctx context.Context, script string) *exec.Cmd {
	c := exec.CommandContext(ctx, "cmd.exe")
	// Args keeps its one-element default so Cmd.String() cannot panic on Args[1:];
	// syscall.StartProcess uses a non-empty CmdLine verbatim and ignores argv.
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /S /C "` + script + `"`}
	return c
}
