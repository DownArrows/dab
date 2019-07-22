package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

func FileOlderThan(path string, max_age time.Duration) (bool, error) {
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

func ErrorsToError(errs []error, sep string) error {
	if len(errs) == 0 {
		return nil
	}
	strs := make([]string, 0, len(errs))
	for _, err := range errs {
		strs = append(strs, err.Error())
	}
	return errors.New(strings.Join(strs, sep))
}

type SemVer [3]byte

func (v SemVer) Major() uint8 {
	return v[0]
}

func (v SemVer) Minor() uint8 {
	return v[1]
}

func (v SemVer) Patch() uint8 {
	return v[2]
}

func (v SemVer) After(ov SemVer) bool {
	return (v.Major() > ov.Major() ||
		v.Major() == ov.Major() && v.Minor() > ov.Minor() ||
		v.Major() == ov.Major() && v.Minor() == ov.Minor() && v.Patch() > ov.Patch())
}

func (v SemVer) Equal(ov SemVer) bool {
	return v.Major() == ov.Major() && v.Minor() == ov.Minor() && v.Patch() == ov.Patch()
}

func (v SemVer) AfterOrEqual(ov SemVer) bool {
	return v.After(ov) || v.Equal(ov)
}

func (v SemVer) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major(), v.Minor(), v.Patch())
}

// Encodes the SemVer on the lower three bytes of a 32 bits integer.
func (v SemVer) ToInt32() int32 {
	return (int32(v.Major()) * 65536) + (int32(v.Minor()) * 256) + int32(v.Patch())
}

func SemVerFromInt32(encoded int32) SemVer {
	major := encoded / 65536

	encoded %= 65536
	minor := encoded / 256

	patch := encoded % 256

	return SemVer{byte(major), byte(minor), byte(patch)}
}

func SliceHasString(slice []string, str string) bool {
	for _, el := range slice {
		if el == str {
			return true
		}
	}
	return false
}
