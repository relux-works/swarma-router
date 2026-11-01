package handoff

import (
	"encoding/binary"
	"os"
	"syscall"
	"unsafe"
)

// fgetattrlist inspects the already-open object, not a second path lookup.
// Darwin ATTR_CMN_EXTENDED_SECURITY returns an attrreference to an ACL blob;
// no blob means no extended ACL. Unknown, truncated or unsupported reads refuse.
// ABI: Apple sys/attr.h and getattrlist(2), ATTR_BIT_MAP_COUNT=5,
// ATTR_CMN_EXTENDED_SECURITY=0x00400000, FSOPT_REPORT_FULLSIZE=4.
func noExtendedACL(f *os.File) bool {
	attrs := struct {
		Count, Reserved                       uint16
		Common, Volume, Directory, File, Fork uint32
	}{Count: 5, Common: 0x00400000}
	var buf [8192]byte
	_, _, errno := syscall.Syscall6(syscall.SYS_FGETATTRLIST, f.Fd(), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 4, 0)
	if errno != 0 {
		return false
	}
	// uint32 total size, int32 relative offset, uint32 ACL length. Even an
	// empty extended ACL is refused; no ACL has a zero-length reference.
	return binary.LittleEndian.Uint32(buf[0:4]) == 12 && binary.LittleEndian.Uint32(buf[8:12]) == 0
}
