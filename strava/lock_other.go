//go:build !unix

package strava

import "os"

// Elsewhere there is no lock: two programs refreshing the same token at the
// same moment is rare, and the cost is logging in again.
func lockFile(*os.File) error   { return nil }
func unlockFile(*os.File) error { return nil }
