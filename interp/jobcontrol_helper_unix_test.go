// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

//go:build unix

package interp_test

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func jobControlStopChild() {
	fmt.Printf("STOP_ID pid=%d pgrp=%d\n", os.Getpid(), syscall.Getpgrp())
	if err := syscall.Kill(os.Getpid(), syscall.SIGTSTP); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Println("STOP_CONTINUED")
	os.Exit(5)
}

func jobControlTTYChild() {
	continued := make(chan os.Signal, 1)
	signal.Notify(continued, syscall.SIGCONT)
	defer signal.Stop(continued)
	go func() {
		<-continued
		fmt.Println("TTY_CONTINUED")
	}()
	fmt.Printf("TTY_ID pid=%d pgrp=%d\n", os.Getpid(), syscall.Getpgrp())
	fmt.Println("TTY_READY")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Printf("TTY_GOT=%s\n", strings.TrimRight(line, "\r\n"))
	os.Exit(7)
}

func jobControlReportShellForeground() {
	foreground, err := unix.IoctlGetInt(int(os.Stdin.Fd()), unix.TIOCGPGRP)
	if err != nil {
		fmt.Fprintf(os.Stderr, "foreground query: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("SHELL_RESTORED pgrp=%d foreground=%d\n", syscall.Getpgrp(), foreground)
}
