//go:build windows

package processlifecycle

import (
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

const windowsBirthTokenScheme = "windows-process-creation-filetime/v1"

func readWindowsProcessIdentity(pid int) (ProcessIdentity, error) {
	if pid <= 0 {
		return ProcessIdentity{}, fs.ErrNotExist
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if err == windows.ERROR_INVALID_PARAMETER {
			return ProcessIdentity{}, fs.ErrNotExist
		}
		return ProcessIdentity{}, err
	}
	defer windows.CloseHandle(handle)
	return windowsProcessIdentity(pid, handle)
}

func currentWindowsProcessIdentity() (ProcessIdentity, error) {
	return windowsProcessIdentity(os.Getpid(), windows.CurrentProcess())
}

func windowsProcessIdentity(pid int, handle windows.Handle) (ProcessIdentity, error) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return ProcessIdentity{}, err
	}
	return ProcessIdentity{
		PID:              pid,
		BirthTokenScheme: windowsBirthTokenScheme,
		BirthToken:       fmt.Sprintf("%08x%08x", created.HighDateTime, created.LowDateTime),
	}, nil
}
