package jman

import "syscall"

const jdtlsConfigDir = "config_mac"

func childProcessAttrs() *syscall.SysProcAttr {
	// Darwin has no parent-death signal; shutdown still terminates the process group.
	return &syscall.SysProcAttr{Setpgid: true}
}
