package workbench

import "os"

// Linux POSIX ACL permissions are bounded by the group-class mode bits checked
// by ownedPrivate; Darwin requires an additional, separate ACL query.
func operationNoExtendedACL(*os.File) bool { return true }
