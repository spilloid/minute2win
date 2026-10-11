package main

import (
	"os"
	"syscall"
	"unsafe"
)

var moveStateFile = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

// Windows cannot FlushFileBuffers on the read-only directory handle used by os.Open.
// Replace the already-flushed temporary file with a write-through move instead.
// Arms are never restored by Load, so even uncertain crash durability cannot revive one.
func replaceStateFile(tmp, target string) error {
	from, err := syscall.UTF16PtrFromString(tmp)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	const replaceExisting = 0x1
	const writeThrough = 0x8
	ok, _, callErr := moveStateFile.Call(uintptr(unsafe.Pointer(from)), uintptr(unsafe.Pointer(to)), replaceExisting|writeThrough)
	if ok == 0 {
		return &os.LinkError{Op: "MoveFileExW", Old: tmp, New: target, Err: callErr}
	}
	return nil
}
