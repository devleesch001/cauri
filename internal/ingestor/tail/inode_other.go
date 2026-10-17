//go:build !unix

package tail

import "os"

// inodeOf has no inode to give outside Unix (os.FileInfo carries none on
// Windows): files are then found by their fingerprint alone, as a copied
// file is. It lets the binary build there for development.
func inodeOf(os.FileInfo) uint64 { return 0 }
