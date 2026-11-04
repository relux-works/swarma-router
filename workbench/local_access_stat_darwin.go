package workbench

import "syscall"

func policyChangeTime(st *syscall.Stat_t) syscall.Timespec { return st.Ctimespec }
