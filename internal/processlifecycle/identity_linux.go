//go:build linux

package processlifecycle

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

func readUnixProcessIdentity(pid int) (ProcessIdentity, error) {
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ProcessIdentity{}, fmt.Errorf("read Linux boot identity: %w", err)
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat") // #nosec G304 -- pid is an integer selected by the lifecycle owner
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ProcessIdentity{}, fs.ErrNotExist
		}
		return ProcessIdentity{}, err
	}
	closeParen := strings.LastIndexByte(string(data), ')')
	if closeParen < 0 || closeParen+2 >= len(data) {
		return ProcessIdentity{}, fmt.Errorf("parse /proc/%d/stat: missing command terminator", pid)
	}
	fields := strings.Fields(string(data[closeParen+2:]))
	const startTimeIndexAfterState = 19 // proc field 22, with this slice starting at field 3
	if len(fields) <= startTimeIndexAfterState || fields[startTimeIndexAfterState] == "" {
		return ProcessIdentity{}, fmt.Errorf("parse /proc/%d/stat: missing start time", pid)
	}
	bootToken := strings.TrimSpace(string(bootID))
	if bootToken == "" {
		return ProcessIdentity{}, fmt.Errorf("read Linux boot identity: empty boot ID")
	}
	return ProcessIdentity{
		PID: pid, BirthTokenScheme: "linux-boot-id+proc-starttime-ticks/v1",
		BirthToken: bootToken + ":" + fields[startTimeIndexAfterState],
	}, nil
}
