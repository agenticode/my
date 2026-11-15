//go:build !unix

package outfile

// writable reports whether this user may create files in dir. Where the
// question cannot be asked, the write itself gives the answer.
func writable(string) bool { return true }
