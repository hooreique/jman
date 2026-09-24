package jman

import "syscall"

const jdtlsConfigDir = "config_linux"

func childProcessAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
}
