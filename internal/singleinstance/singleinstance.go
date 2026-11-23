// Package singleinstance guarantees one NeuroRift process per machine and
// routes "launch again" requests to the running instance.
//
// WHY NOT WAILS' BUILT-IN SingleInstance (issue #32)?
//
//  1. Its Windows notify path is broken: the first instance's event window
//     is a message-only window (HWND_MESSAGE parent), and plain FindWindow
//     never finds message-only windows — a second instance can never wake
//     the first one. With close-to-tray that means: click the shortcut,
//     nothing happens, ever.
//  2. Its notify uses blocking SendMessage: a hung first instance (message
//     loop dead) makes every future launch block forever inside
//     application.New — after logging "Platform Info", with no window and
//     no error. Precisely the "the app feels like it never started" report.
//  3. It offers no recovery: a zombie process holding the mutex poisons
//     every subsequent launch until the owner kills processes by hand.
//
// This package fixes all three:
//
//   - Acquire() implements the mutex dance correctly (CreateMutex +
//     ERROR_ALREADY_EXISTS) and, when a previous instance exists but is
//     UNRESPONSIVE, terminates it and takes over — startup is self-healing.
//   - ListenOnMainThread() creates a top-level (FindWindow-visible) hidden
//     event window on the message-pump thread; second instances wake the
//     first one through it with SendMessageTimeout, so neither side can
//     ever block forever.
//
// AI-DEVELOPER NOTES:
//   - Call order: Acquire() in main() before application.New();
//     ListenOnMainThread() from the Wails ServiceStartup hook (it needs the
//     pump thread, which only exists once app.Run() has started).
//   - The mutex name reuses Wails' "wails-app-<id>-sim" convention so a
//     v1.1.0 process already holding it is still detected by v1.2.0.
package singleinstance

import "errors"

// ErrAlreadyRunning is returned by Acquire when a healthy previous instance
// was notified successfully; the caller should exit immediately (exit code 0)
// and let the running instance show its window.
var ErrAlreadyRunning = errors.New("another NeuroRift instance is already running")

// ErrLockHeld is returned by Acquire when the mutex stays held by a process
// that could not be terminated — typically an instance running elevated or in
// another session, where OpenProcess(PROCESS_TERMINATE) is denied. The caller
// should offer the user a recovery choice instead of dying (issue #89).
var ErrLockHeld = errors.New("single-instance lock held by a process we cannot terminate")
