package hermessnapshot

import "github.com/nmxmxh/ovasabi_foundation/server-kit/go/kernellane"

// cloneFile promotes an artifact by delegating to the shared kernel accelerator
// lane in server-kit/go/kernellane.
//
// The clone ladder — reflink (FICLONE), then copy_file_range, then a portable
// userspace copy — used to live here. It now has one implementation that every
// caller shares, so a kernel lane added or fixed in kernellane reaches snapshot
// promotion, bulk transfer, and any future consumer at once. The returned lane
// names the mechanism that actually ran; the string values are unchanged
// ("reflink", "copy_file_range", "userspace"), so PromoteLatest's contract and
// its callers are untouched.
func cloneFile(dst, src string) (string, error) {
	return kernellane.CloneFile(dst, src)
}
