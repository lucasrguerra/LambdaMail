//go:build !unix

package main

import "errors"

// freeDiskPercent has no portable implementation outside unix. Returning an
// error makes the preflight report the check as skipped rather than inventing
// a number.
func freeDiskPercent(string) (float64, error) {
	return 0, errors.New("free disk space check is only implemented on unix")
}
