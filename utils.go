package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"time"
)

// ErrSentinel is an error used to signal that there's been an error but has been dealt with out of band.
var ErrSentinel = errors.New("sentinel error, you SHOULD NOT be seeing that")

// FileOlderThan tells if the file at path is older than maxAge.
func FileOlderThan(path string, maxAge time.Duration) (bool, error) {
	stat, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}

	timeDiff := time.Now().Sub(stat.ModTime())
	return (timeDiff > maxAge), nil
}

// SemVer is a data structure for semantic versioning.
type SemVer [3]byte

// Major version.
func (v SemVer) Major() uint8 {
	return v[0]
}

// Minor version.
func (v SemVer) Minor() uint8 {
	return v[1]
}

// Patch version.
func (v SemVer) Patch() uint8 {
	return v[2]
}

// After tests if the current SemVer is after the other SemVer passed as an argument.
func (v SemVer) After(ov SemVer) bool {
	return (v.Major() > ov.Major() ||
		v.Major() == ov.Major() && v.Minor() > ov.Minor() ||
		v.Major() == ov.Major() && v.Minor() == ov.Minor() && v.Patch() > ov.Patch())
}

// Equal tests if the current SemVer is equal to the other SemVer passed as an argument.
func (v SemVer) Equal(ov SemVer) bool {
	return v.Major() == ov.Major() && v.Minor() == ov.Minor() && v.Patch() == ov.Patch()
}

// AfterOrEqual tests if the current SemVer is after or equal to the other SemVer passed as an argument.
func (v SemVer) AfterOrEqual(ov SemVer) bool {
	return v.After(ov) || v.Equal(ov)
}

// String representation of the semantic version.
func (v SemVer) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major(), v.Minor(), v.Patch())
}

// ToInt encodes the SemVer on the lower three bytes of a 32 bits integer.
func (v SemVer) ToInt() int {
	return (int(v.Major()) * 65536) + (int(v.Minor()) * 256) + int(v.Patch())
}

// SemVerFromInt decodes a SemVer encoded by SemVer.ToInt.
func SemVerFromInt(encoded int) SemVer {
	major := encoded / 65536

	encoded %= 65536
	minor := encoded / 256

	patch := encoded % 256

	return SemVer{byte(major), byte(minor), byte(patch)}
}

// Sort allows to sort anything without copy-pasting nor generics.
type Sort struct {
	Len  func() int
	Less func(int, int) bool
	Swap func(int, int)
}

// Do the sort.
func (s Sort) Do() {
	sort.Sort(sorter{sort: s})
}

type sorter struct {
	sort Sort
}

func (s sorter) Len() int {
	return s.sort.Len()
}

func (s sorter) Less(i, j int) bool {
	return s.sort.Less(i, j)
}

func (s sorter) Swap(i, j int) {
	s.sort.Swap(i, j)
}

// SliceHasString tests if the slice of strings has a string.
func SliceHasString(slice []string, str string) bool {
	for _, el := range slice {
		if el == str {
			return true
		}
	}
	return false
}
