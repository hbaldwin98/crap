//go:build windows

package clioutput

import "golang.org/x/sys/windows"

func replaceFile(source, destination string) (bool, error) {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return false, err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return false, err
	}
	flags := uint32(windows.MOVEFILE_REPLACE_EXISTING | windows.MOVEFILE_WRITE_THROUGH)
	if err := windows.MoveFileEx(from, to, flags); err != nil {
		return false, err
	}
	return true, nil
}
