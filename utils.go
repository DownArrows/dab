package main

import (
	"fmt"
	"os"
	"time"
)

func autopanic(err error) {
	if err != nil {
		panic(err)
	}
}

func fileOlderThan(path string, max_age time.Duration) (bool, error) {
	stat, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}

	time_diff := time.Now().Sub(stat.ModTime())
	return (time_diff > max_age), nil
}

type version [3]byte

func (v version) Major() uint8 {
	return v[0]
}

func (v version) Minor() uint8 {
	return v[1]
}

func (v version) BugFix() uint8 {
	return v[2]
}

func (v version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major(), v.Minor(), v.BugFix())
}

// Encodes the version on the lower three bytes of a 32 bits integer.
func (v version) ToInt32() int32 {
	return (int32(v.Major()) * 65536) + (int32(v.Minor()) * 256) + int32(v.BugFix())
}

func versionFromInt32(encoded int32) version {
	major := encoded / 65536

	encoded %= 65536
	minor := encoded / 256

	bugfix := encoded % 256

	return version{byte(major), byte(minor), byte(bugfix)}
}
